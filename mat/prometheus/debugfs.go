package main

import (
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"

	"github.com/prometheus/prometheus/tsdb"
)

func parseDebugfs(path string, ts int64, m []string, data []byte, a tsdb.Appender) error {
	phy := m[2]

	var netdev string
	if strings.HasPrefix(m[3], "netdev:") {
		netdev = strings.TrimPrefix(m[3], "netdev:")
	}

	filename := m[4]

	var varPrefix string
	var station string

	var fileParsers *map[string]parseFn
	if !strings.HasPrefix(filename, "stations/") {
		varPrefix = ""
		fileParsers = &parsers
	} else {
		m := regexp.MustCompile(`stations/([0-9a-f:]+)/(.*)$`).FindStringSubmatch(filename)
		if m == nil {
			log.Fatalf("Regexp (station) no match: %s", filename)
		}

		varPrefix = "stations_"
		if name, ok := stations[m[1]]; ok {
			station = name
		} else {
			station = m[1]
		}
		filename = m[2]
		fileParsers = &parsersStation
	}

	parser, ok := (*fileParsers)[filename]
	if ok {
		if parser == nil {
			if *v {
				log.Printf("Skipping %s", filename)
			}
		} else {
			if *v {
				log.Printf("Input %q:\n%s", path, data)
			}

			parser(string(data), func(key string, vls map[string]string, value float64) {
				varValue := varPrefix + filename
				if key != "" {
					varValue += "_" + strings.ReplaceAll(key, "-", "_")
				}

				ls := map[string]string{
					"job":      "ath9k",
					"phy":      phy,
					"__name__": varValue,
					"var":      varValue,
				}
				for k, v := range vls {
					ls[k] = v
				}
				if netdev != "" {
					ls["netdev"] = netdev
				}
				if station != "" {
					ls["station"] = station
				}

				addPoint(ls, ts, value, a)
			})
		}
	} else {
		if *v {
			log.Printf("Ignored: %q:\n%s", path, data)
		}
	}
	return nil
}

type emitFn func(key string, vls map[string]string, value float64)

func parseOne(text string, emit emitFn) {
	text = strings.ToLower(text)

	// ath9k/ani, ath9k/spectral_scan_ctl, ath9k/tpc
	if strings.Contains(text, "enable") {
		emit("", nil, 1.0)
	} else if strings.Contains(text, "disable") {
		emit("", nil, 0.0)
	} else {
		value, err := strconv.ParseInt(strings.TrimSpace(text), 0, 64)
		if err != nil {
			log.Printf("Warning: %q: %s", text, err)
			return
		}
		emit("", nil, float64(value))
	}
}

func parseFlags(text string, emit emitFn) {
	for _, l := range strings.Split(text, "\n") {
		if l != "" {
			emit(l, nil, 1.0)
		}
	}
}

func parseRows(text string, emit emitFn) {
	for _, l := range strings.Split(text, "\n") {
		fs := strings.Split(l, ":")
		if len(fs) >= 2 {
			key := strings.ReplaceAll(strings.TrimSpace(fs[0]), " ", "_")

			if strings.TrimSpace(fs[1]) == "ENABLED" {
				// ath9k/ani: "ANI: ENABLED"
				emit(key, nil, 1.0)
			} else if strings.TrimSpace(fs[1]) == "DISABLED" {
				emit(key, nil, 0.0)
			} else if fs[1] == "" {
				// ath9k/interrupt: "SYNC_CAUSE stats:" header
			} else {
				value, err := strconv.Atoi(strings.TrimSpace(fs[1]))
				if err != nil {
					panic(fmt.Sprintf("%q: %s", l, err))
				}
				emit(key, nil, float64(value))
			}
		}
	}
}

func parseTable(text string, emit emitFn) {
	lines := strings.Split(text, "\n")

	header := lines[0]
	widths := []int{19, 11, 11, 10, 10}
	col := 0
	cols := []string{}
	for _, w := range widths {
		cols = append(cols, header[col:col+w])
		col += w
	}

	for _, l := range lines[1:] {
		if len(l) > 0 {
			fs := strings.Split(l, ":")
			key := strings.ReplaceAll(strings.TrimSpace(fs[0]), " ", "_")

			col := 0
			for i, w := range widths {
				if i >= 1 {
					value, err := strconv.Atoi(strings.TrimSpace(l[col : col+w]))
					if err != nil {
						log.Fatalf("Atoi: %q (in %q): %s", l[col:col+w], l, err)
					}

					emit(key, map[string]string{"col": strings.TrimSpace(cols[i])}, float64(value))
				}
				col += w
			}
		}
	}
}

func parseRCStats(text string, emit emitFn) {
	lines := strings.Split(text, "\n")
	for _, l := range lines[3 : len(lines)-4] {
		rate := strings.TrimSpace(l[21 : 21+6])
		labels := map[string]string{"rate": rate}

		bestA := l[14]
		if bestA == 'A' {
			emit("best_A", labels, 1.0)
		}

		success, err := strconv.Atoi(strings.TrimSpace(l[100:109]))
		if err != nil {
			panic(fmt.Sprintf("%q: %s", l, err))
		}
		emit("rate_success", labels, float64(success))

		attempts, err := strconv.Atoi(strings.TrimSpace(l[112:]))
		if err != nil {
			panic(fmt.Sprintf("%q: %s", l, err))
		}
		emit("rate_attempts", labels, float64(attempts))
	}

	var total bool
	for _, l := range lines[len(lines)-4:] {
		m := regexp.MustCompile(`Total.*ideal (\d+).*lookaround (\d+)`).FindStringSubmatch(l)
		if m != nil {
			ideal, err := strconv.Atoi(m[1])
			if err != nil {
				panic(fmt.Sprintf("%q: %s", l, err))
			}
			emit("ideal", nil, float64(ideal))
			lookaround, err := strconv.Atoi(m[2])
			if err != nil {
				panic(fmt.Sprintf("%q: %s", l, err))
			}
			emit("lookaround", nil, float64(lookaround))
			total = true
			break
		}
	}
	if !total {
		panic(fmt.Sprintf("%q: missing total", lines))
	}
}

type parseFn func(text string, emit emitFn)

var parsers map[string]parseFn = map[string]parseFn{
	"antenna_diversity":      parseOne,
	"chanbw":                 parseOne,
	"diag":                   parseOne,
	"gpio_mask":              parseOne,
	"gpio_val":               parseOne,
	"regval":                 parseOne,
	"rx_chainmask":           parseOne,
	"spectral_count":         parseOne,
	"spectral_fft_period":    parseOne,
	"spectral_period":        parseOne,
	"spectral_scan_ctl":      parseOne,
	"spectral_short_repeat":  parseOne,
	"tpc":                    parseOne,
	"tx_chainmask":           parseOne,
	"ap_power_level":         parseOne,
	"dtim_count":             parseOne,
	"flags":                  parseOne,
	"num_buffered_multicast": parseOne,
	"num_mcast_sta":          parseOne,
	"num_sta_ps":             parseOne,
	"rc_rateidx_mask_2ghz":   parseOne,
	"rc_rateidx_mask_5ghz":   parseOne,
	"state":                  parseOne,
	"interrupt":              parseRows,
	"phy_err":                parseRows,
	"recv":                   parseRows,
	"reset":                  parseRows,
	"xmit":                   parseTable,
	"ani":                    parseRows,
	"dot11ACKFailureCount":   parseOne,
	"dot11FCSErrorCount":     parseOne,
	"dot11RTSFailureCount":   parseOne,
	"dot11RTSSuccessCount":   parseOne,
}

var parsersStation map[string]parseFn = map[string]parseFn{
	"flags":           parseFlags,
	"last_ack_signal": parseOne,
	"rx_duplicates":   parseOne,
	"rx_fragments":    parseOne,
	"tx_filtered":     parseOne,
	"rc_stats":        parseRCStats,
}
