package main

import (
	"log"
	"strings"

	"github.com/prometheus/prometheus/tsdb"
)

func resolve(ip string) string {
	if host, ok := hosts[ip]; ok {
		return host
	}
	return ip
}

func parseConntrack(path string, ts int64, data []byte, a tsdb.Appender) error {
	for _, l := range strings.Split(string(data), "\n") {
		fs := strings.Fields(l)
		if len(fs) >= 4 && fs[0] == "ipv4" && fs[2] == "tcp" {
			fm := map[string]string{}
			nsrcs := 0
			for _, l := range fs {
				kv := strings.SplitN(l, "=", 2)
				if len(kv) == 2 {
					if kv[0] == "src" {
						nsrcs++
					}
					var dir string
					if nsrcs == 1 {
						dir = "o"
					} else if nsrcs == 2 {
						dir = "r"
					}
					fm[dir+kv[0]] = kv[1]
				}
			}

			ls := map[string]string{}

			var dirs []string
			if fm["osrc"] == "135.23.201.114" || fm["rdst"] == "135.23.201.114" {
				dirs = []string{"tx", "rx"}
			} else if fm["odst"] == "135.23.201.114" || fm["rsrc"] == "135.23.201.114" {
				dirs = []string{"rx", "tx"}
			} else {
				log.Printf("Unknown direction: %q", l)
				continue
			}

			nsrcs = 0
			for _, l := range fs {
				kv := strings.SplitN(l, "=", 2)
				if len(kv) == 2 {
					if kv[0] == "src" {
						nsrcs++
					}

					if nsrcs == 1 {
						ls["src"] = resolve(fm["osrc"])
						ls["sport"] = fm["osport"]
						ls["dst"] = resolve(fm["odst"])
						ls["dport"] = fm["odport"]
					} else if nsrcs == 2 {
						ls["src"] = resolve(fm["odst"])
						ls["sport"] = fm["odport"]
						ls["dst"] = resolve(fm["osrc"])
						ls["dport"] = fm["osport"]
					} else {
						panic(nsrcs)
					}

					switch kv[0] {
					case "packets", "bytes":
						ls["__name__"] = "conntrack_" + dirs[nsrcs-1] + "_" + kv[0]
						addPoint(ls, ts, float64(atoi(kv[1])), a)
					}
				}
			}
		}
	}
	return nil
}
