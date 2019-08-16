//go:generate wget -N ftp://ftp.ncdc.noaa.gov/pub/data/gsod/2018/gsod_2018.tar ftp://ftp.ncdc.noaa.gov/pub/data/noaa/isd-history.txt && tar -C 2018 xf gsod_2018.tar
//
// Search through 10000 worldwide weather stations for closest matching daily
// maximum and minimum daily temperatures.
//
// http://nanobit.org/climate/
package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"flag"
	"golang.org/x/sync/semaphore"
	"html/template"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	_ "net/http/pprof"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

var dev = flag.Bool("dev", false, "Parse only first 100 stations, reload templates on every fetch.")

type Station string

func (s Station) Name() string {
	return strings.TrimSpace(string(s)[13:42])
}

func (s Station) Country() string {
	return string(s)[43:45]
}

func (s Station) LatLng() string {
	return strings.Replace(string(s)[57:73], " ", ",", 1)
}

var stations map[string]Station

type GSOD struct {
	Name string
	Max1 float32 // sum(max)/n
	Max2 float32 // sum(max^2)/n
	Min1 float32
	Min2 float32
	N    int
}

func (g *GSOD) USAF() string {
	return g.Name[0:6]
}

func (g *GSOD) WBAN() string {
	return g.Name[7:12]
}

func (g *GSOD) Station() Station {
	return stations[g.Name]
}

func xdev(x1, x2 float32) float32 {
	// 95% CI
	return float32(2.0 * math.Sqrt(float64(x2-x1*x1)))
}

func (g *GSOD) MaxDev() float32 {
	return xdev(g.Max1, g.Max2)
}

func (g *GSOD) MinDev() float32 {
	return xdev(g.Min1, g.Min2)
}

var gsod []GSOD

func readStations() map[string]Station {
	file, err := os.Open("isd-history.txt")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	stations := make(map[string]Station)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 1 && "0" <= line[0:1] && line[0:1] <= "9" {
			stations[line[0:6]+"-"+line[7:12]] = Station(line)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Fatal(err)
	}

	return stations
}

func mustParseTemp(s string) float32 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 32)
	if err != nil {
		log.Fatal(err)
	}
	return (float32(f) - 32.0) * 5.0 / 9.0
}

func readGSOD() []GSOD {
	files, err := ioutil.ReadDir("2018/")
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Found %d stations", len(files))

	// Limit open files, else "too many open files".
	maxWorkers := int64(20)
	sem := semaphore.NewWeighted(maxWorkers)
	mu := &sync.Mutex{}
	gsod := make([]GSOD, 0)
	skip := 0

	if *dev {
		files = files[:100]
	}

	for _, file := range files {
		if err := sem.Acquire(context.Background(), 1); err != nil {
			log.Fatal(err)
		}
		go func(file os.FileInfo) {
			defer sem.Release(1)

			var station GSOD
			f, err := os.Open("2018/" + file.Name())
			if err != nil {
				log.Fatal(err)
			}
			defer f.Close()

			gf, err := gzip.NewReader(f)
			if err != nil {
				log.Fatal(err)
			}
			defer gf.Close()

			scanner := bufio.NewScanner(gf)
			scanner.Scan() // header

			max1 := float32(0.0)
			max2 := float32(0.0)
			min1 := float32(0.0)
			min2 := float32(0.0)
			n := 0

			for scanner.Scan() {
				line := scanner.Text()

				max := mustParseTemp(line[103:108])
				max1 += max
				max2 += max * max

				min := mustParseTemp(line[111:116])
				min1 += min
				min2 += min * min

				n += 1
			}

			if n > 300 {
				station.Name = file.Name()[0:12]
				station.N = n
				n := float32(n)
				station.Max1 = max1 / n
				station.Max2 = max2 / n
				station.Min1 = min1 / n
				station.Min2 = min2 / n
				if err := scanner.Err(); err != nil {
					log.Fatal(err)
				}

				mu.Lock()
				defer mu.Unlock()
				gsod = append(gsod, station)
			} else {
				mu.Lock()
				defer mu.Unlock()
				skip += 1
			}
		}(file)
	}

	if err := sem.Acquire(context.Background(), maxWorkers); err != nil {
		log.Fatal(err)
	}

	log.Printf("Read %d stations, skipped %d", len(gsod), skip)

	return gsod
}

func getOrEmpty(r *http.Request, key string) string {
	if _, ok := r.URL.Query()[key]; ok {
		return r.URL.Query().Get(key)
	} else {
		return ""
	}
}

func find(max string, min string) ([]*GSOD, error) {
	maxt, err := strconv.ParseFloat(max, 32)
	if err != nil {
		return nil, err
	}

	mint, err := strconv.ParseFloat(min, 32)
	if err != nil {
		return nil, err
	}

	stas := make([]*GSOD, len(gsod))
	for i, _ := range gsod {
		stas[i] = &gsod[i]
	}

	dist := func(t0 float32, t1 float32, t2 float32, n float32) float32 {
		// sum((t0 - t)^2) / n
		// = t0^2 - 2*t0*sum(t)/n + sum(t^2)/n
		// = (t0 - mean(t))^2 + var(t)
		return t0*t0 - 2*t0*t1/n + t2/n
	}

	d := func(i int) float32 {
		sta := stas[i]
		return dist(float32(maxt), sta.Max1, sta.Max2, float32(sta.N)) +
			dist(float32(mint), sta.Min1, sta.Min2, float32(sta.N))
	}

	sort.Slice(stas, func(i, j int) bool { return d(i) < d(j) })

	return stas[:10], nil
}

func loadTemplate() *template.Template {
	return template.Must(template.New("").ParseFiles("index.html"))
}

var t = loadTemplate()

type IndexData struct {
	Max     string
	Min     string
	Error   string
	Results []*GSOD
}

func main() {
	flag.Parse()

	log.SetOutput(os.Stderr)
	stations = readStations()
	gsod = readGSOD()
	http.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		if regexp.MustCompile(`^/static/[a-z0-9.]+$`).MatchString(r.URL.Path) {
			http.ServeFile(w, r, r.URL.Path[1:])
		} else {
			http.NotFound(w, r)
		}
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			v := IndexData{}
			v.Max = getOrEmpty(r, "max")
			v.Min = getOrEmpty(r, "min")

			if v.Max != "" && v.Min != "" {
				stas, err := find(v.Max, v.Min)
				if err != nil {
					v.Error = err.Error()
				} else {
					v.Results = stas
				}
			}

			if *dev {
				t = loadTemplate()
			}
			err := t.ExecuteTemplate(w, "index.html", v)
			if err != nil {
				log.Printf("Template: %s", err)
				http.Error(w, "500 internal server error", http.StatusInternalServerError)
				return
			}
		} else {
			http.NotFound(w, r)
		}
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "127.0.0.1:8080"
		log.Printf("Defaulting to port %s", port)
	} else {
		port = os.Getenv("HOST") + ":" + port
	}
	log.Printf("Listening on port %s", port)
	log.Fatal(http.ListenAndServe(port, nil))
}
