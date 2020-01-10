package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cheggaaa/pb/v3"
	"github.com/prometheus/prometheus/pkg/timestamp"
	"github.com/prometheus/prometheus/tsdb"
	"github.com/prometheus/prometheus/tsdb/labels"
)

const maxInt = int(^uint(0) >> 1)

var debugfs = flag.Bool("debugfs", true, "Parse /sys/debug/kernel files.")
var nl = flag.Bool("nl", true, "Parse netlink (/nl80211) files.")
var conntrack = flag.Bool("conntrack", true, "Parse conntrack (/proc/net/nf_conntrack).")

var add = flag.Bool("add", false, "Add to TSDB.")
var v = flag.Bool("v", false, "Show raw input text, and parsed individual timeseries points as they would be added.")

var startback = flag.Duration("startback", 365*24*time.Hour, "Start importing tar.gz that contain data starting this duration in the past from the current time.")
var lastn = flag.Int("lastn", maxInt, "Import only last N tar.gz.")

var points int64

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

type logger struct{}

func (l *logger) Log(kv ...interface{}) error {
	for i := 0; i < len(kv); {
		if i != 0 {
			fmt.Printf(" ")
		}

		var q string
		if _, ok := kv[i+1].(string); ok {
			q = "\""
		}

		fmt.Printf("%s=%s%v%s", kv[i], q, kv[i+1], q)
		i += 2
	}
	fmt.Printf("\n")
	return nil
}

var debugfsRe = regexp.MustCompile(`^(\d+)/sys/kernel/debug/ieee80211/(phy\d+)/(ath9k|netdev:\w+|statistics)/(.*)$`)
var conntrackRe = regexp.MustCompile(`^(\d+)/proc/net/nf_conntrack$`)
var nl80211Re = regexp.MustCompile(`^(\d+)/nl80211/(\w+)/stations/\d+$`)

func addPoint(ls map[string]string, ts int64, value float64, a tsdb.Appender) {
	if *v {
		log.Printf("Add(%s, %d, %f)", ls, ts, value)
	}

	if *add {
		_, err := a.Add(labels.FromMap(ls), ts, value)
		if err != nil {
			log.Fatalf("Add(%s, %d, %f): %s", ls, ts, value, err)
		}
	}

	points++
}

func parseFile(path string, r io.Reader, a tsdb.Appender) error {
	m := debugfsRe.FindStringSubmatch(path)
	if m != nil {
		if !*debugfs {
			return nil
		}
		t, err := strconv.Atoi(m[1])
		if err != nil {
			log.Fatalf("Bad timestamp: %s (in %s)", m[1], path)
		}
		ts := timestamp.FromTime(time.Unix(0, 0).Add(time.Duration(t) * time.Microsecond))

		data, err := ioutil.ReadAll(r)
		if err != nil {
			return fmt.Errorf("Read: %w", err)
		}

		return parseDebugfs(path, ts, m, data, a)
	}

	m = nl80211Re.FindStringSubmatch(path)
	if m != nil {
		if !*nl {
			return nil
		}
		t, err := strconv.Atoi(m[1])
		if err != nil {
			log.Fatalf("Bad timestamp: %s (in %s)", m[1], path)
		}
		ts := timestamp.FromTime(time.Unix(0, 0).Add(time.Duration(t) * time.Microsecond))

		// TODO: fix if style -> if err = ...; err != nil { ... }
		data, err := ioutil.ReadAll(r)
		if err != nil {
			return fmt.Errorf("Read: %w", err)
		}

		return parseNetlink(path, ts, m, data, a)
	}

	m = conntrackRe.FindStringSubmatch(path)
	if m != nil {
		if !*conntrack {
			return nil
		}
		t, err := strconv.Atoi(m[1])
		if err != nil {
			log.Fatalf("Bad timestamp: %s (in %s)", m[1], path)
		}
		ts := timestamp.FromTime(time.Unix(0, 0).Add(time.Duration(t) * time.Microsecond))

		// TODO: fix if style -> if err = ...; err != nil { ... }
		data, err := ioutil.ReadAll(r)
		if err != nil {
			return fmt.Errorf("Read: %w", err)
		}

		return parseConntrack(path, ts, data, a)
	}
	log.Fatalf("Regexp no match: %s", path)
	return nil
}

func parseTar(tarfi string, a tsdb.Appender) {
	f, err := os.Open(tarfi)
	if err != nil {
		log.Fatalf("Open(%s): %s", tarfi, err)
	}
	defer f.Close()

	br := bufio.NewReaderSize(f, 1<<20)

	zf, err := gzip.NewReader(br)
	if err != nil {
		log.Fatalf("gzip.NewReader(%s): %s", tarfi, err)
	}

	tr := tar.NewReader(zf)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("Tar next (%s): %s", tarfi, err)
			break
		}

		if hdr.Typeflag == tar.TypeReg {
			err := parseFile(hdr.Name, tr, a)
			if err != nil {
				log.Printf("%s:%s: %v", tarfi, hdr.Name, err)
				break
			}
		}
	}
}

func pathStartTime(path string) time.Time {
	base := filepath.Base(path)
	t, err := strconv.Atoi(base[:strings.Index(base, ".")])
	if err != nil {
		panic(err)
	}
	return time.Unix(int64(t/1000000), int64((t%1000000)*1000))
}

func pathEndTime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		panic(err)
	}
	return st.ModTime()
}

func main() {
	flag.Parse()

	go func() {
		log.Println(http.ListenAndServe("localhost:7070", nil))
	}()

	db, err := tsdb.Open("data", &logger{}, nil, tsdb.DefaultOptions)
	if err != nil {
		log.Fatalf("tsdb.Open: %s", err)
	}

	tars, err := filepath.Glob("nyc-cache/prom/*.tar.gz")
	if err != nil {
		log.Fatalf("ReadDir: %s", err)
	}

	i := 0
	for pathEndTime(tars[i]).Before(time.Now().Add(-*startback)) {
		i++
		if i >= len(tars) {
			log.Fatalf("startback matched zero tars")
		}
	}
	log.Printf("Skipping %d tars due to startback", i)
	tars = tars[i:]

	log.Printf("Keeping last %d tars", *lastn)
	tars = tars[max(len(tars)-*lastn, 0):]

	startTime, endTime := pathStartTime(tars[0]), pathEndTime(tars[len(tars)-1])
	log.Printf("Importing %s to %s (%.01f hours)",
		startTime, endTime, endTime.Sub(startTime).Hours())

	bar := pb.Full.Start(len(tars))
	for _, tarfi := range tars {
		a := db.Appender()
		parseTar(tarfi, a)
		err = a.Commit()
		if err != nil {
			log.Fatalf("Commit: %s", err)
		}
		bar.Increment()
	}
	bar.Finish()

	log.Printf("Extracted %d points", points)

	if err != nil {
		log.Fatalf("Walk: %s", err)
	}

	err = db.Close()
	if err != nil {
		log.Fatalf("Close: %s", err)
	}
}
