# Debugging ath9k using Prometheus+Grafana

Some wifi clients have been experiencing flaky connection with my OpenWrt router. `/sys/kernel/debug/ieee80211` offers [lots of debug information](https://elixir.bootlin.com/linux/latest/source/drivers/net/wireless/ath/ath9k/debug.c) but there's no built-in system in OpenWrt to collect and display these metrics. [`nl80211`](https://wireless.wiki.kernel.org/en/developers/documentation/nl80211) (which backs `iw` command) also contains lots of information (per client signal strength).

[Prometheus](https://prometheus.io/) is a monitoring system and time series database with efficient storage, powerful querying and good compatibility with graphing systems ([Grafana](https://grafana.com/)). However, it is not designed for this use case.

![Screenshot](https://i.redd.it/s01mxnaa16241.png)

## Constraints

*  Observe short-lived events (< 10 seconds). Therefore, need high collection frequency.
*  Low powered CPU (MIPS 680 MHz).
*  Limited on-device storage (128 MB total RAM, 70 MB free, must leave space for other services).
*  No always-on machine on local network. Intermittent connectivity with off-site cheap storage.

## Overview

Device only does collection and buffering. All parsing, data processing and graphing is done off-site, on a powerful machine.

On device:
*  [`collect.c`](collect.c) walks `/sys/kernel/debug/ieee80211`, `nl80211`, `nf_conntrack` and generates timestamped snapshots. Currently every 2 seconds.
*  `/tmp/prom` contains a circular buffer of snapshots (5 MB). For the current collection set (~100 `debugfs` files, 600 Prometheus metrics) this is equivalent to ~20 minutes of collection during which device can operate offline without data loss. Tolerance to disconnected operation is limited only by local storage.

Off-site:
*  Cron job runs `rsync` to copy snapshots to long-term storage. Every 10 minutes.
*  Keep as much data as you want / can afford, for example 1 GB (~2 days).

As needed for investigation:
*  Run [`bulkimport.go`](bulkimport.go) to parse snapshots and import into fresh TSDB.
*  Run Prometheus to serve TSDB data (disable collection entirely); run Grafana to serve dashboard.

Unfortunately, this does not show metrics in real-time, but it can still help debug problems.

When updating parsers, re-run `bulkimport.go` on archived snapshots, and metrics will appear for historical data.

## Future Work

Collect `nl80211` data (like `iw wlan0 station dump`).
