# High Speed File Transfer

High end server hardware holds the promise of very fast data processing but it can be difficult to realize the full potential. Most software is designed around the assumption that "I/O time" dominates, but this can turn out to be wrong in case of fast storage devices and fast network links. To achieve full hardware performance, it may be necessary to tweak system performance parameters, use profiling tools to identify bottlenecks, and use specialized file transfer tools that are designed to overcome these botllenecks.

In particular, this article describes an attempt to transfer a large [MongoDB](https://www.mongodb.com/) database between two physical servers with large RAID arrays and 10 Gigabit Ethernet links. For testing, we'll be transferring a single large file (`collection-20-522933037738215512.wt` - 333 GB). Conventional tools (`rsync`) only achieve ~100 MB/s transfer rate, but with a little experimentation and tuning, it's possible to saturate the network link (1 GB/s).

[TOC]

## Hardware

--> `store2`: Dell R510 (CPU: [Xeon](https://en.wikipedia.org/wiki/Xeon) X5660 @ 2.8 GHz
  Storage: Dell PowerEdge H700 [RAID-6](https://en.wikipedia.org/wiki/Standard_RAID_levels#RAID_6) of 8 disks)
  Network Adapter: [Intel X520-DA2 10GbE PCI-e Dual Port SFP+](https://canservers.com/dell-intel-x520-da2-10gbe-pci-e-dual-port-sfp.html?utm_source=github&utm_medium=link&utm_campaign=filetransfer)
[View on canservers.com](https://canservers.com/dell-r510-custom-configuration.html?utm_source=github&utm_medium=link&utm_campaign=filetransfer)

--> [10 Gigabit Ethernet](https://en.wikipedia.org/wiki/10_Gigabit_Ethernet)

--> `db1`: Dell R820 (CPU: [Xeon](https://en.wikipedia.org/wiki/Xeon) E5-4640 @ 2.2 GHz
  Storage: Dell PowerEdge H710 [RAID-6](https://en.wikipedia.org/wiki/Standard_RAID_levels#RAID_6) of 12 disks)
  Network Adapter: [Intel X520-DA2 10GbE PCI-e Dual Port SFP+](https://canservers.com/dell-intel-x520-da2-10gbe-pci-e-dual-port-sfp.html?utm_source=github&utm_medium=link&utm_campaign=filetransfer)
[View on canservers.com](https://canservers.com/dell-poweredge-r820-2u-4-cpu-server.html?utm_source=github&utm_medium=link&utm_campaign=filetransfer)

## OS Cache

Before jumping into benchmarks, remember that the OS caches data read from disk to avoid future disk operations. This can give misleading results when repeating the same benchmark, as the data will be cached in RAM for the second trial:

```
# first read
store2$ dd if=collection-20-522933037738215512.wt of=/dev/null bs=10M count=100
100+0 records in
100+0 records out
1048576000 bytes (1.0 GB, 1000 MiB) copied, 1.28683 s, 815 MB/s

# second read
store2$ dd if=collection-20-522933037738215512.wt of=/dev/null bs=10M count=100
100+0 records in
100+0 records out
1048576000 bytes (1.0 GB, 1000 MiB) copied, 0.338524 s, 3.1 GB/s
```

This isn't a realistic benchmark, as it isn't truly reading data from disk both times. During the actual file transfer, the data will not be already present in cache, so we are interested in the speed for actually reading it from disk. There are many ways to remove the effect of the cache but here are two particular methods.

### drop_cache

[`/proc/sys/vm/drop_cache`](https://linux-mm.org/Drop_Caches) drops cached data for *all files* in the system. This will impact performance of other running applications. For a normal system, it usually takes only a few seconds for the OS to re-read necessary data into RAM:

```
# cached read
store2$ dd if=collection-20-522933037738215512.wt of=/dev/null bs=10M count=100
100+0 records in
100+0 records out
1048576000 bytes (1.0 GB, 1000 MiB) copied, 0.338524 s, 3.1 GB/s

# flush cache
store2$ echo 1 | sudo tee /proc/sys/vm/drop_caches
1

# uncached read - better estimate of disk performance
store2$ dd if=collection-20-522933037738215512.wt of=/dev/null bs=10M count=100
100+0 records in
100+0 records out
1048576000 bytes (1.0 GB, 1000 MiB) copied, 1.28683 s, 815 MB/s
```

### O_DIRECT

Files may be opened with the flag [O_DIRECT](http://man7.org/linux/man-pages/man2/open.2.html) which bypasses OS caches for this specific file. It also bypasses certain parts of kernel filesystem code, which can improve performance, as we will see later. The application must explicitly support it (so it's not always practical for production use), but the `dd` utility certainly does support it:

```
store2$ dd if=collection-20-522933037738215512.wt iflag=direct of=/dev/null bs=10M count=100
100+0 records in
100+0 records out
1048576000 bytes (1.0 GB, 1000 MiB) copied, 0.868665 s, 1.2 GB/s
```

However, note that this will certainly **decrease performance** if used in production on files that tend to be re-read (database files) as this will force the OS to re-read them from physical disk each time.

## OS Network Stack

When an application sends data over the network, the OS buffers some amount of data on behalf of the application. This amount is high enough to keep data flowing quickly, but low enough to keep kernel memory usage under control. At high transfer rates, it is useful to have the OS buffer more data, as this reduces the number of transfers between application and OS, which do consume some CPU.

The following settings do not themselves increase buffers, but **allow applications to request** larger buffers. The application must still explicitly request it.

```
store2$ cat tcp.conf
# allow testing with buffers up to 128MB
net.core.rmem_max = 134217728
net.core.wmem_max = 134217728
# increase Linux autotuning TCP buffer limit to 64MB
net.ipv4.tcp_rmem = 4096 87380 67108864
net.ipv4.tcp_wmem = 4096 65536 67108864

store2$ sudo sysctl -w -p tcp.conf
```

Reference: [Network / TCP / UDP Tuning](https://wwwx.cs.unc.edu/~sparkst/howto/network_tuning.php)

## Benchmark

First, let's get a sense of the hardware capabilities of each component of the system: source storage, network and destination storage.

### Source Storage

```
store2$ $ dd if=collection-20-522933037738215512.wt iflag=direct of=/dev/null bs=10M count=100
100+0 records in
100+0 records out
1048576000 bytes (1.0 GB, 1000 MiB) copied, 0.868665 s, 1.2 GB/s
```

### Network

```
db1$ iperf -c 10.10.1.202
------------------------------------------------------------
Client connecting to 10.10.1.202, TCP port 5001
TCP window size: 1.61 MByte (default)
------------------------------------------------------------
[  3] local 10.10.1.155 port 45762 connected with 10.10.1.202 port 5001
[ ID] Interval       Transfer     Bandwidth
[  3]  0.0-10.0 sec  10.9 GBytes  9.37 Gbits/sec
```

### Target Storage

```
$ dd if=/dev/zero of=zero oflag=direct bs=10M count=100
100+0 records in
100+0 records out
1048576000 bytes (1.0 GB, 1000 MiB) copied, 0.773024 s, 1.4 GB/s
```

So, both storage arrays seem to be able to deliver enough read/write bandwidth to saturate the network. We should expect ~1 GB/s transfer rate.

## Tools

### rsync

`rsync` is the traditional tool for file transfers, but it doesn't achieve a very good transfer rate:

```
db1$ rsync -avP store2:/nfs/mongo/collection-20-522933037738215512.wt .
receiving incremental file list
collection-20-522933037738215512.wt
    879,591,424   0%  103.99MB/s    0:55:45  ^C
```

Note that a significant amount of CPU time is spent in `ssh`, probably due to encryption:

```
    PID USER      PR  NI    VIRT    RES    SHR S  %CPU  %MEM     TIME+ COMMAND                        
  87567 cat       20   0   14704   9596   6308 S  45.3   0.0   0:25.33 ssh store2 rsync --server --s+
  87568 cat       20   0    7180   2080   1516 S  30.2   0.0   0:18.98 rsync -avP store2:/nfs/mongo/+
```

Our network is a trusted datacenter network, so encryption is not needed.

### GridFTP

US Department of Energy has the following presentation with several potential tools for high speed file transfer: [Bulk Data Transfer Techniques for High-Speed Wide-Area Networks](https://fasterdata.es.net/assets/fasterdata/Tierney-bulk-data-transfer-tutorial-Sept09.pdf). It even boasts ["GridFTP from ANL has everything needed to fill the network pipe"](https://fasterdata.es.net/assets/fasterdata/Tierney-bulk-data-transfer-tutorial-Sept09.pdf#page=30).

[Globus GridFTP Server](https://docs.globus.org/globus-connect-server-installation-guide/man/globus-gridftp-server/), and its companion client program [globus-url-copy](https://linux.die.net/man/1/globus-url-copy) are indeed very full-featured file transfer programs, with features like: adjustable disk block size, adjustable TCP buffer size, parallel TCP streams, inter-server transfers (similar to FTP's [FXP](https://en.wikipedia.org/wiki/File_eXchange_Protocol)).

It's also very convenient to install, as it's included in Debian (and thus Ubuntu) in [package `globus-gass-copy-progs`](https://packages.debian.org/buster/globus-gass-copy-progs). However, its official site states ["Globus Toolkit is Retired"](https://toolkit.globus.org/) and the [GitHub repository](https://github.com/globus/globus-toolkit) last saw meaningul commits in June 2019. "Old" isn't necessary bad though, so let's give it a try:

```
store2$ sudo sysctl -w vm.drop_caches=1  # bypass OS cache
  && globus-gridftp-server \
    -debug -port 9123 \
    -aa \  # anonymous access - only if your network is trusted
    -d ERROR,WARN,INFO,TRANSFER \
    -l /dev/stderr -no-inetd \
    -bs 32M  # block size for disk reads
[17511] Tue May 19 06:49:06 2020 :: Configuration read from /etc/gridftp.conf.
[17511] Tue May 19 06:49:06 2020 :: Server started in daemon mode.

db1$ rm -f collection-20-522933037738215512.wt && \
  globus-url-copy -v \
    -vb \  # show transfer progress, with transfer rate
    -bs 32M \  # block size, disk writes 
    -tcp-bs 256M \
    -p 2  # two parallel TCP streams
    ftp://anonymous@10.10.1.202:9123/nfs/mongo/collection-20-522933037738215512.wt collection-20-522933037738215512.wt
Source: ftp://anonymous@10.10.1.202:9123/nfs/mongo/
Dest:   file:///home/cat/
  collection-20-522933037738215512.wt

^C 3321888768 bytes       526.61 MB/sec avg       539.72 MB/sec inst
Cancelling copy...
```

Looks like GridFTP falls short of saturating the network connection. `atop` shows that `globus-url-copy` is using a full core on the target machine, and that's the bottleneck:

```
ATOP - db1                          2020/05/19  06:55:36                          y--a----------                            1s elapsed
PRC |  sys    1.18s |  user   0.04s |  #proc    806 |  #trun      2 |  #tslpi  1044  | #tslpu     0  | #zombie    1  | #exit      0  |
CPU |  sys     107% |  user      4% |  irq      18% |  idle   7938% |  wait      0%  | ipc notavail  | curf 1.57GHz  | curscal   ?%  |
CPL |  avg1    1.38 |  avg5    0.68 |  avg15   0.33 |               |  csw     1794  | intr    9207  |               | numcpu    80  |
MEM |  tot     1.0T |  free   85.9G |  cache 510.0G |  buff  909.4M |  slab   18.6G  | shmem   2.3M  | vmbal   0.0M  | hptot   0.0M  |
SWP |  tot     8.0G |  free    7.8G |               |               |                |               | vmcom 397.3G  | vmlim 511.9G  |
PSI |  cs     0/0/0 |  ms     0/0/0 |  mf     0/0/0 |  is     8/8/4 |  if     8/8/4  |               |               |               |
NET |  transport    |  tcpi   15061 |  tcpo    3163 |  udpi       0 |  udpo       0  | tcpao      0  | tcppo      0  | tcprs      0  |
NET |  network      |  ipi    15079 |  ipo     3163 |  ipfrw      0 |  deliv  15079  |               | icmpi      0  | icmpo      0  |
NET |  enp5s0f  44% |  pcki  370277 |  pcko    3149 |  sp   10 Gbps |  si 4484 Mbps  | so 1662 Kbps  | erri       0  | erro       0  |
NET |  eno2      0% |  pcki      22 |  pcko      14 |  sp 1000 Mbps |  si   21 Kbps  | so   28 Kbps  | erri       0  | erro       0  |
No timer set; waiting for manual trigger ('t').....
NPROCS    SYSCPU    USRCPU     VSIZE      RSIZE     PSIZE    SWAPSZ      RDDSK     WRDSK     RNET      SNET     CPU    CMD        1/38
     1     1.03s     0.00s    136.7M     135.2M        0K        0K         0K        0K        0         0    100%    globus-url-cop
     2     0.09s     0.03s    29636K     25964K        0K        0K         0K        0K        0         0     12%    atop
     1     0.03s     0.00s        0K         0K        0K        0K         0K        0K        0         0      3%    ksoftirqd/28
```

*  Source disk: 430 MB/s
*  Source CPU: 51%
*  Network: 3900 Mbps
*  Target CPU: 100%
*  Target disk: 450 MB/s

Let's see what `globus-url-copy` is doing during this time...

```
db1$ strace -p $(pgrep globus-url-copy)
...
recvfrom(10, "\0\0\0\0\0\2\0\0\0\0\0\0\1&\0\0\0", 17, 0, NULL, NULL) = 17
recvfrom(11, "0\201>\16\211'\4a \21\200\0 \1\2006\306\1\0`\212\306\1\20\203\200\300\344d1\306\0"..., 25162445, 0, NULL, NULL) = 14465>
select(12, [3 7 9 10 11], [], NULL, {tv_sec=0, tv_usec=419880}) = 1 (in [10], left {tv_sec=0, tv_usec=419872})
recvfrom(10, "\0\0\0\0\0\0\0\0[\316\3\0\0\0\0\0\374n\0\0^\0\0\0\7\5\0\0\0000\0\0"..., 33554432, 0, NULL, NULL) = 4534900
select(12, [3 7 9 10 11], [], NULL, {tv_sec=0, tv_usec=415846}) = 2 (in [10 11], left {tv_sec=0, tv_usec=415843})
recvfrom(10, "\t-\306\301\304!\306\16\237&&\226w\24/ikCLo\26\25/)\213\16~\16\10I'd"..., 29019532, 0, NULL, NULL) = 165072
recvfrom(11, "his\22\310\"\fon P\5`\34 Of Fish\16\260!\10en \35a("..., 23715893, 0, NULL, NULL) = 1450896
select(12, [3 7 9 10 11], [], NULL, {tv_sec=0, tv_usec=414386}) = 2 (in [10 11], left {tv_sec=0, tv_usec=414383})
recvfrom(10, "_help\315~Ab\16R\17\252~\6ZN\0i\321aH\0R\16\314 \22\27\27\10e"..., 28854460, 0, NULL, NULL) = 1652168
recvfrom(11, "\25\307\24/nyiCr\36\346\10\22\207\25\1G\4 F\5G\4 R.G\0\306O\1\20"..., 22264997, 0, NULL, NULL) = 1450896
...
lseek(8, 4932501504, SEEK_SET)          = 4932501504
write(8, "\0\0\0\0\0\0\0\0[\316\3\0\0\0\0\0\374n\0\0^\0\0\0\7\5\0\0\0000\0\0"..., 33554432) = 33554432
recvfrom(10, "\0\0\0\0\0\2\0\0\0\0\0\0\1*\0\0\0", 17, 0, NULL, NULL) = 17
recvfrom(11, "\0s\261\204\t\246\"\26\"\5\247\0 \22Z\225\26z\217\0o\16m\26Q.\36\v[\16>\202"..., 2052365, 0, NULL, NULL) = 1459584
select(12, [3 7 9 10 11], [], NULL, {tv_sec=0, tv_usec=327021}) = 2 (in [10 11], left {tv_sec=0, tv_usec=327013})
recvfrom(10, "ys-Z\201\314\241\205-Z\1$\r\311\"\320!\201,\24Maui A\21<6B\1\0"..., 33554432, 0, NULL, NULL) = 3482259
recvfrom(11, "\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0\0"..., 592781, 0, NULL, NULL) = 592781
select(12, [3 7 9 10 11], [8], NULL, {tv_sec=0, tv_usec=323973}) = 3 (in [10 11], out [8], left {tv_sec=0, tv_usec=323969})
lseek(8, 4831838208, SEEK_SET)          = 4831838208
write(8, "\10\16$\35\26\203\v\32\325\24\1T\16\7\27\0s\32-,*\237N\0r\272O\0\26\2\35\16"..., 33554432) = 33554432
...
```

Looks like it's reading from two sockets (fds 10 and 11), accumulating into 32 MB buffers (as specified by `-bs`) and writing them to disk using single calls to `write`.

Let's look at a whole-system CPU profile (including time spent in the kernel) to see if there is any clear bottleneck. `sleep 10` is just an unusual way to say "record for 10 seconds":

```
db1$ sudo perf record -g -a sleep 10
[ perf record: Woken up 13 times to write data ]
[ perf record: Captured and wrote 6.022 MB perf.data (31744 samples) ]

db1$ sudo perf report
Samples: 31K of event 'cycles', Event count (approx.): 36994092467
  Children      Self  Command          Shared Object                   Symbol
-   67.92%     0.02%  globus-url-copy  [kernel.kallsyms]               [k] entry_SYSCALL_64_after_hwframe  ◆
   - 67.91% entry_SYSCALL_64_after_hwframe                                                                 ▒
      - 67.90% do_syscall_64                                                                               ▒
         + 38.40% __x64_sys_write                                                                          ▒
         + 29.19% __x64_sys_recvfrom                                                                       ▒

# and an expanded view

Samples: 31K of event 'cycles', Event count (approx.): 36994092467
  Children      Self  Command          Shared Object                   Symbol
-   67.92%     0.02%  globus-url-copy  [kernel.kallsyms]               [k] entry_SYSCALL_64_after_hwframe
   - 67.91% entry_SYSCALL_64_after_hwframe
      - 67.90% do_syscall_64
         - 38.40% __x64_sys_write
              ksys_write
              vfs_write
            - __vfs_write
               - 38.40% new_sync_write
                    ext4_file_write_iter
                  - __generic_file_write_iter
                     - 38.32% generic_perform_write
                        - 21.88% ext4_da_write_begin
                           - 15.38% grab_cache_page_write_begin
                              - 15.26% pagecache_get_page
                                 - 9.57% __page_cache_alloc
                                    - 9.52% alloc_pages_current
                                       - 9.32% __alloc_pages_nodemask
                                          - 9.01% get_page_from_freelist
                                               7.85% clear_page_erms
                                               0.58% rmqueue
                                 - 5.07% add_to_page_cache_lru
                                    - 4.13% __add_to_page_cache_locked
                                         1.79% mem_cgroup_try_charge
                                    - 0.57% lru_cache_add
                                       - __lru_cache_add
                                            0.54% pagevec_lru_move_fn
                           - 4.63% ext4_block_write_begin
                              - 2.96% ext4_da_get_block_prep
                                 - 2.79% ext4_da_map_blocks.constprop.0
                                      0.95% ext4_es_insert_delayed_block
                                      0.55% ext4_es_lookup_extent
                              - 1.21% create_empty_buffers
                                 - 1.12% alloc_page_buffers
                                    - 0.96% alloc_buffer_head
                                         0.69% kmem_cache_alloc
                           - 1.22% __ext4_journal_start_sb
                              - 1.08% jbd2__journal_start
                                   0.73% start_this_handle
                        - 7.16% ext4_da_write_end
                           - 6.04% generic_write_end
                              - 3.55% __mark_inode_dirty
```

Looks like a lot of time spent in receiving from network, and writing to disk. We can either try to increase parallelism (spread the load to multiple cores) or improve efficiency (reduce overheads).

*   **Increasing parallelism**: This requires the application to be built to take advantage of multiple cores (usually multi-threaded). Unfortunately (from the `strace` output) it seems `globus-url-copy` uses a simple select-recv-write loop and its design is strictly single-threaded.
*   **Improve efficiency**: Since CPU time is spread out across many different places (`recvfrom`, `write`, ext4 filesystem code, page cache), it's not easy to find any single tweak to improve overall performance. One option would be `O_DIRECT`, which bypasses most filesystem code and page cache, but again the application must explicitly support this (due to buffer alignment requirements). `globus-url-copy` does *not* have any command-line parameter to enable `O_DIRECT`. We tried [enabling it from behind its back using a `LD_PRELOAD`](https://github.com/gburd/libdirectio) but the I/O ultimately failed, because the tool is not designed with the necessary buffer alignment.

Unfortunately, while GridFTP can use parallel TCP streams (which benefit long-distance WAN links), that does not help here. It is **single-threaded** so it easily becomes CPU-bound on short-distance high-bandwidth links.

References:

*  [Bulk Data Transfer Techniques for High-Speed Wide-Area Networks](https://fasterdata.es.net/assets/fasterdata/Tierney-bulk-data-transfer-tutorial-Sept09.pdf)
*  [A Tutorial on Configuring and Deploying GridFTP for Managing Data Movement in Grid/HPC Environments](https://www.mcs.anl.gov/~mlink/tutorials/GridFTPTutorialSlides.pdf) (Argonne National Laboratory, University of Chicago)
*  [Efficient file transfers with globus-url-copy](https://www.westgrid.ca/support/grid_tools/globus_url_copy) (WestGrid Canada)
*  [Globus GridFTP Server Manual Page](https://docs.globus.org/globus-connect-server-installation-guide/man/globus-gridftp-server/)
*  [globus-url-copy(1) - Linux man page](https://linux.die.net/man/1/globus-url-copy)


## fast-data-transfer

[fdt](https://github.com/fast-data-transfer/fdt) is another potential tool for fast file transfer. However, it runs on Java, which can make deployment more difficult and adds more moving parts in case of debugging. The last meaningful commit in the [GitHub repository](https://github.com/fast-data-transfer/fdt) was from September 2019, so it may be more actively maintained, but we didn't explore it further.

## bbcp

[bbcp](https://www.slac.stanford.edu/~abh/bbcp/) is a high performance file transfer tool written in C++ at Stanford Linear Accelerator Center (SLAC). It is shown having good performance in the Intel whitepaper "[Maximizing File Transfer Performance Using 10Gb Ethernet and Virtualization](https://www.intel.com/content/dam/support/us/en/documents/network/sb/fedexcasestudyfinal.pdf)".

The original authors' [SLAC GitHub repository](https://github.com/slaclab/bbcp) seems abandoned (April 2015), but there are many community forks and the most active seems to be [gyohng's repository](https://github.com/gyohng/bbcp), last commit April 2019.

There is no readily available Debian package, but it's relatively easy to download and install from source. If you have machines with different distribution versions, built it on the _oldest_, as this makes it easier to run the binary on the newer distribution.

```
store2$ make -j10
...
Creating executable ../bin/amd64_linux/bbcp ...
Make done.

store2$ ../bin/amd64_linux/bbcp -h
Usage:   bbcp [Options] [Inspec] Outspec

Options: [-a [dir]] [-A] [-b [+]bf] [-B bsz] [-c [lvl]] [-C cfn] [-D] [-d path]
...
```

To be honest, `bbcp` was a little tricky to get running at first. This is in part due to the network configuration: the two machines are connected by two different network links, one slower and the main 10 Gb link. These are on different physical interfaces and IP subnets, so it's crucial to use the right one:

```
db1$ ip addr show up
...
3: eno2: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc mq state UP group default qlen 1000
    link/ether c8:1f:66:d0:b3:5c brd ff:ff:ff:ff:ff:ff
    inet 192.168.2.179/24 brd 192.168.2.255 scope global dynamic eno2
...
7: enp5s0f1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 qdisc mq state UP group default qlen 1000
    link/ether 90:e2:ba:ec:03:a1 brd ff:ff:ff:ff:ff:ff
    inet 10.10.1.155/20 brd 10.10.15.255 scope global enp5s0f1
```

`enp5s0f1` (`10.10.1.155/20`) is the correct, high speed link.

### Background

To set up a transfer, `bbcp` starts a copy of itself on the remote machine using `ssh`, which connects back to the intiator over a separate data channel. This is a nice reuse of `ssh` for the control channel, it takes advantage of existing authentication you might have for `ssh` (password, public key, etc).

For the data channel, `bbcp` looks up the local address and passes it to the remote host ("callback host") for connecting. When the local host has multiple addresses, `bbcp` might pick the wrong one. In our case, it was picking the slow link. There are some traces of support for setting an explicit callback host on the command-line but it was incompletely implemented in the initial release, fixed in the `andrew-krasny` fork, then broken again when it was merged into `gyohng`. In the end, the `andrew-krasny` fork allows setting an explicit callback host using `-H`, but `gyohng` has more other features in general.

```
db1$ ./bbcp \
   -f \  # force overwrite, easier to run multiple tests
   -s 1 \  # one TCP stream (data channel) to start
   -H 10.10.1.155 \  # override local address (high speed link)
   -P 2 \  # print progress and speed periodically
   -S 'ssh -x -a %4 %I -l %U %H src/bbcp/bin/amd64_linux/bbcp' \  # if bbcp is not in $PATH
   -w 32M \  # large TCP buffers
   10.10.1.202:/nfs/mongo/collection-20-522933037738215512.wt .
bbcp: Creating ./collection-20-522933037738215512.wt
bbcp: 200606 04:35:03  0% done; 749.6 MB/s
bbcp: 200606 04:35:04  0% done; 742.2 MB/s
bbcp: 200606 04:35:05  0% done; 704.9 MB/s
```

Pretty good! But still not the theoretical maximum that our disks and network should be able to provide.

### Profiling

Since `bbcp` is multi-threaded, it helps to look at CPU usage by thread. With a command-line option (`-y`) we can see each row's CPU usage on a separate thread:

```
db1$ atop -y 1
ATOP - db1                          2020/06/18  04:29:13                          y-------------                            1s elapsed
PRC |  sys    1.84s |  user   0.04s |  #proc    836 |  #trun      3 |  #tslpi   906  | #tslpu     0  | #zombie    0  | #exit      0  |
CPU |  sys     174% |  user      4% |  irq      58% |  idle   7824% |  wait      0%  | ipc notavail  | curf 2.07GHz  | curscal   ?%  |
CPL |  avg1    0.66 |  avg5    0.85 |  avg15   0.46 |               |  csw     5356  | intr    7112  |               | numcpu    80  |
MEM |  tot     1.0T |  free   60.7G |  cache 523.6G |  buff  762.0M |  slab    7.7G  | shmem   4.8M  | vmbal   0.0M  | hptot   0.0M  |
SWP |  tot     8.0G |  free    5.7G |               |               |                |               | vmcom 420.3G  | vmlim 511.9G  |
PSI |  cs     0/0/0 |  ms     0/0/1 |  mf     0/0/1 |  is     0/1/3 |  if     0/1/3  |               |               |               |
DSK |           sda |  busy      0% |  read       0 |  write      3 |  KiB/w      5  | MBr/s    0.0  | MBw/s    0.0  | avio 1.33 ms  |
NET |  transport    |  tcpi   20245 |  tcpo    4131 |  udpi       0 |  udpo       0  | tcpao      0  | tcppo      0  | tcprs      0  |
NET |  network      |  ipi    20272 |  ipo     4131 |  ipfrw      0 |  deliv  20272  |               | icmpi      0  | icmpo      0  |
NET |  enp5s0f  70% |  pcki  579970 |  pcko    4122 |  sp   10 Gbps |  si 7018 Mbps  | so 2232 Kbps  | erri       0  | erro       0  |
NET |  eno2      0% |  pcki      35 |  pcko      10 |  sp 1000 Mbps |  si   37 Kbps  | so   35 Kbps  | erri       0  | erro       0  |

    PID       TID   SYSCPU    USRCPU    VGROW    RGROW    RUID       EUID       ST    EXC    THR   S    CPUNR    CPU   CMD         1/2
 134201         -    1.66s     0.01s       0K       0K    cat        cat        --      -      4   R       69   169%   bbcp
 134201    134201    1.00s     0.01s       0K       0K    cat        cat        --      -      1   R       69   100%   bbcp
 134201    134203    0.66s     0.01s       0K       0K    cat        cat        --      -      1   R       78    68%   bbcp
    280         -    0.13s     0.00s       0K       0K    root       root       --      -      1   S       44    13%   ksoftirqd/44
 134199         -    0.05s     0.02s       0K       0K    cat        cat        --      -      1   R       19     7%   atop
 133999         -    0.00s     0.01s       0K       0K    cat        cat        --      -      1   S       28     1%   sshd
```

Looks like one thread (134201) in particular is using a full core, and is probably the bottleneck, but we don't know what it's doing.

With a [small change to bbcp](https://github.com/gyohng/bbcp/pull/1) (which also needed to be ported to the `andrew-krasny` branch, for `-H` support), we can identify each thread, and see a clearer picture in `top`:
```
db1$ atop -y 1
ATOP - db1                          2020/06/18  04:30:28                          y-------------                            1s elapsed
PRC |  sys    1.63s |  user   0.04s |  #proc    835 |  #trun      3 |  #tslpi   906  | #tslpu     0  | #zombie    0  | #exit      0  |
CPU |  sys     162% |  user      7% |  irq      19% |  idle   7856% |  wait      0%  | ipc notavail  | curf 1.75GHz  | curscal   ?%  |
CPL |  avg1    1.25 |  avg5    1.00 |  avg15   0.55 |               |  csw     5024  | intr   21716  |               | numcpu    80  |
MEM |  tot     1.0T |  free   63.8G |  cache 520.6G |  buff  762.9M |  slab    7.6G  | shmem   4.8M  | vmbal   0.0M  | hptot   0.0M  |
SWP |  tot     8.0G |  free    5.7G |               |               |                |               | vmcom 420.4G  | vmlim 511.9G  |
PSI |  cs     0/0/0 |  ms     0/0/1 |  mf     0/0/0 |  is     1/4/3 |  if     1/4/3  |               |               |               |
NET |  transport    |  tcpi   27225 |  tcpo    4822 |  udpi       0 |  udpo       0  | tcpao      0  | tcppo      0  | tcprs      0  |
NET |  network      |  ipi    27223 |  ipo     4822 |  ipfrw      0 |  deliv  27223  |               | icmpi      0  | icmpo      0  |
NET |  enp5s0f  82% |  pcki  679760 |  pcko    4809 |  sp   10 Gbps |  si 8213 Mbps  | so 2542 Kbps  | erri       0  | erro       0  |
NET |  eno2      0% |  pcki      30 |  pcko      13 |  sp 1000 Mbps |  si   27 Kbps  | so   37 Kbps  | erri       0  | erro       0  |

    PID       TID   SYSCPU    USRCPU    VGROW    RGROW    RUID       EUID       ST    EXC    THR   S    CPUNR    CPU   CMD         1/1
 134223         -    1.59s     0.01s       0K       0K    cat        cat        --      -      4   R       31   162%   bbcp
 134223    134223    0.99s     0.01s       0K       0K    cat        cat        --      -      1   R       31   100%   bbcp
 134223    134225    0.59s     0.00s       0K       0K    cat        cat        --      -      1   R       60    60%   bbcp_Net2Buff
 134199         -    0.04s     0.03s       0K       0K    cat        cat        --      -      1   R        3     7%   atop
   1626         -    0.00s     0.00s       0K       0K    elastics   elastics   --      -     96   S       37     0%   java
   1626      2152    0.00s     0.00s       0K       0K    elastics   elastics   --      -      1   S       59     0%   GC Thread#14
```

Looks like the main thread is the bottleneck. Let's use `perf` to see what exactly it's doing (including any time spent in kernel mode - "system time").

```
db1$ $ sudo perf record -g -a sleep 10
[ perf record: Woken up 48 times to write data ]
[ perf record: Captured and wrote 14.426 MB perf.data (71752 samples) ]

db1$ sudo perf report
Samples: 71K of event 'cycles', Event count (approx.): 57767588115
  Children      Self  Command          Shared Object               Symbol
-   41.45%     0.00%  bbcp             libc-2.31.so                [.] __libc_start_main                                             ◆
     __libc_start_main                                                                                                               ▒
     main                                                                                                                            ▒
     bbcp_Protocol::Request                                                                                                          ▒
     bbcp_Node::RecvFile                                                                                                             ▒
   - bbcp_File::Write_All                                                                                                            ▒
      - 41.45% bbcp_File::Write_Normal                                                                                               ▒
         - 41.21% __libc_pwrite64                                                                                                    ▒
            - 41.18% entry_SYSCALL_64_after_hwframe                                                                                  ▒
               - 41.17% do_syscall_64                                                                                                ▒
                  - 41.16% __x64_sys_pwrite64                                                                                        ▒
                     - 41.16% ksys_pwrite64                                                                                          ▒
                        - 41.15% vfs_write                                                                                           ▒
                           - 41.12% __vfs_write                                                                                      ▒
                              - 41.11% new_sync_write                                                                                ▒
                                 - 41.10% ext4_file_write_iter                                                                       ▒
                                    - 41.09% __generic_file_write_iter                                                               ▒
                                       - 40.87% generic_perform_write                                                                ▒
                                          - 25.64% ext4_da_write_begin                                                               ▒
                                             - 18.22% grab_cache_page_write_begin                                                    ▒
                                                - 18.00% pagecache_get_page                                                          ▒
                                                   - 10.33% __page_cache_alloc                                                       ▒
                                                      - 10.25% alloc_pages_current                                                   ▒
                                                         - 10.16% __alloc_pages_nodemask                                             ▒
                                                            - 9.79% get_page_from_freelist                                           ▒
                                                                 7.95% clear_page_erms                                               ▒
                                                                 1.09% rmqueue                                                       ▒
                                                   - 6.87% add_to_page_cache_lru                                                     ▒
                                                      - 4.88% __add_to_page_cache_locked                                             ▒
                                                           1.00% mem_cgroup_try_charge                                               ▒
                                                           0.58% _raw_spin_lock_irq                                                  ▒
                                                      - 1.04% lru_cache_add                                                          ▒
                                                         - __lru_cache_add                                                           ▒
                                                            - 1.02% pagevec_lru_move_fn                                              ▒
                                                                 0.80% __pagevec_lru_add_fn                                          ▒
                                                        0.62% __lru_cache_add                                                        ▒
                                                     0.53% find_get_entry                                                            ▒
                                             - 5.52% ext4_block_write_begin                                                          ▒
                                                - 3.56% ext4_da_get_block_prep                                                       ▒
                                                   - 3.34% ext4_da_map_blocks.constprop.0                                            ▒
                                                        0.99% ext4_da_reserve_space                                                  ▒
                                                        0.84% ext4_es_insert_delayed_block                                           ▒
                                                        0.60% ext4_es_lookup_extent                                                  ▒
                                                - 1.15% create_empty_buffers                                                         ▒
                                                   - 0.97% alloc_page_buffers                                                        ▒
                                                      - 0.82% alloc_buffer_head                                                      ▒
                                                           0.69% kmem_cache_alloc                                                    ▒
                                             - 1.20% __ext4_journal_start_sb                                                         ▒
                                                - 1.01% jbd2__journal_start                                                          ▒
                                                     0.55% start_this_handle                                                         ▒
                                          - 11.80% ext4_da_write_end                                                                 ▒
                                             - 9.90% generic_write_end                                                               ▒
                                                - 5.24% __mark_inode_dirty                                                           ▒
                                                   - 4.97% ext4_dirty_inode                                                          ▒
                                                      - 4.35% ext4_mark_inode_dirty                                                  ▒
                                                         - 3.23% ext4_mark_iloc_dirty                                                ▒
                                                            - 3.07% ext4_do_update_inode                                             ▒
                                                               - 1.68% ext4_inode_csum_set                                           ▒
                                                                  - 1.49% ext4_inode_csum.isra.0                                     ▒
                                                                     - 1.04% crypto_shash_update                                     ▒
                                                                          crc32c_pcl_intel_update                                    ▒
                                                         - 1.01% ext4_reserve_inode_write                                            ▒
                                                              0.70% __ext4_get_inode_loc                                             ▒
                                                - 4.34% block_write_end                                                              ▒
                                                   - 4.26% __block_commit_write.isra.0                                               ▒
                                                      - 3.95% mark_buffer_dirty                                                      ▒
                                                         - 3.29% __set_page_dirty                                                    ▒
                                                            - 1.32% _raw_spin_lock_irqsave                                           ▒
                                                                 native_queued_spin_lock_slowpath                                    ▒
                                                              0.96% account_page_dirtied                                             ▒
                                                              0.76% __xa_set_mark                                                    ▒
                                             - 0.81% __ext4_journal_stop                                                             ▒
                                                  0.75% jbd2_journal_stop                                                            ▒
                                               0.52% unlock_page                                                                     ▒
                                          - 2.22% iov_iter_copy_from_user_atomic                                                     ▒
                                               2.15% copy_user_enhanced_fast_string                                                  ▒
```

Looks like a significant amount of time is spent maintaining page cache, ext4 delayed allocation and ext4 inode data structures, and just plain data copying (`copy_user_enhanced_fast_string`). These data structures support important features of the OS and filesystem (eg. page cache avoids re-reading data multiple times from disk).

However, in this very specific case, for writing a single large file only once to disk, this logic just adds overhead. As we saw [earlier](#o_direct), O_DIRECT can be used to bypass most of this logic, and `bbcp` has an [option (`-u`) to set `O_DIRECT`](https://www.slac.stanford.edu/~abh/bbcp/#_Toc392015148), so let's try it:

```
db1$ ./bbcp \
  ...
  -u st \  # unbuffered I/O at both source and target
  ...
bbcp: 200618 04:40:04  3% done; 959.9 MB/s
bbcp: 200618 04:40:05  4% done; 961.2 MB/s
bbcp: 200618 04:40:06  4% done; 963.0 MB/s
bbcp: 200618 04:40:07  4% done; 962.4 MB/s
```

Much better! Also, `top` shows that `bbcp` is no longer CPU-bound:

```
db1$ atop -y 1
P - db1                          2020/06/18  04:40:36                          y-------------                            1s elapsed
PRC |  sys    0.69s |  user   0.08s |  #proc    834 |  #trun      1 |  #tslpi   907  | #tslpu     1  | #zombie    0  | #exit      0  |
CPU |  sys      67% |  user      7% |  irq      33% |  idle   7945% |  wait      5%  | ipc notavail  | curf 1.37GHz  | curscal   ?%  |
CPL |  avg1    1.00 |  avg5    1.52 |  avg15   1.18 |               |  csw    32011  | intr   22477  |               | numcpu    80  |
MEM |  tot     1.0T |  free  126.4G |  cache 458.3G |  buff  752.6M |  slab    7.5G  | shmem   4.8M  | vmbal   0.0M  | hptot   0.0M  |
SWP |  tot     8.0G |  free    5.7G |               |               |                |               | vmcom 420.3G  | vmlim 511.9G  |
PSI |  cs     0/0/0 |  ms     0/0/1 |  mf     0/0/1 |  is  35/19/10 |  if  35/19/10  |               |               |               |
DSK |           sda |  busy     99% |  read       0 |  write   3765 |  KiB/w    254  | MBr/s    0.0  | MBw/s  933.9  | avio 0.26 ms  |
NET |  transport    |  tcpi   25090 |  tcpo   16458 |  udpi       0 |  udpo       0  | tcpao      0  | tcppo      0  | tcprs      0  |
NET |  network      |  ipi    25091 |  ipo    16459 |  ipfrw      0 |  deliv  25091  |               | icmpi      0  | icmpo      0  |
NET |  enp5s0f  81% |  pcki  677783 |  pcko   16450 |  sp   10 Gbps |  si 8193 Mbps  | so 8689 Kbps  | erri       0  | erro       0  |
NET |  eno2      0% |  pcki      37 |  pcko      11 |  sp 1000 Mbps |  si   43 Kbps  | so   35 Kbps  | erri       0  | erro       0  |

    PID       TID   SYSCPU    USRCPU    VGROW    RGROW    RUID       EUID       ST    EXC    THR   S    CPUNR    CPU   CMD         1/1
 134259         -    0.64s     0.03s       0K       0K    cat        cat        --      -      4   R       50    68%   bbcp
 134259    134259    0.20s     0.01s       0K       0K    cat        cat        --      -      1   D       50    21%   bbcp
 134259    134261    0.43s     0.02s       0K       0K    cat        cat        --      -      1   S       28    45%   bbcp_Net2Buff
 134259    134262    0.00s     0.00s       0K       0K    cat        cat        --      -      1   S       30     0%   bbcp_MonProc
 134257         -    0.03s     0.03s       0K       0K    cat        cat        --      -      1   R       63     6%   atop
   4803         -    0.00s     0.01s       0K       0K    mongodb    mongodb    --      -     33   S       61     1%   mongod
   4803      4835    0.00s     0.01s       0K       0K    mongodb    mongodb    --      -      1   S       18     1%   ftdc
     12         -    0.00s     0.01s       0K       0K    root       root       --      -      1   I        3     1%   rcu_sched
```

For good measure, let's look at `perf` for what that thread using 68% CPU is doing:

```
db1$ sudo perf record -g -a sleep 10
[ perf record: Woken up 48 times to write data ]
[ perf record: Captured and wrote 14.426 MB perf.data (71752 samples) ]

db1$ sudo perf report
Samples: 42K of event 'cycles', Event count (approx.): 22151951783
  Children      Self  Command          Shared Object               Symbol
...
-   11.64%     0.00%  bbcp             bbcp                        [.] bbcp_Protocol::Request                                        ▒
     bbcp_Protocol::Request                                                                                                          ▒
     bbcp_Node::RecvFile                                                                                                             ▒
   - bbcp_File::Write_All                                                                                                            ▒
      - 11.62% bbcp_File::Write_Direct                                                                                               ▒
         - 11.28% __libc_pwrite64                                                                                                    ◆
            - 11.11% entry_SYSCALL_64_after_hwframe                                                                                  ▒
               - 11.10% do_syscall_64                                                                                                ▒
                  - 11.02% __x64_sys_pwrite64                                                                                        ▒
                     - 11.02% ksys_pwrite64                                                                                          ▒
                        - 10.97% vfs_write                                                                                           ▒
                           - 10.87% __vfs_write                                                                                      ▒
                              - 10.85% new_sync_write                                                                                ▒
                                 - 10.81% ext4_file_write_iter                                                                       ▒
                                    - 10.77% __generic_file_write_iter                                                               ▒
                                       - 10.67% generic_file_direct_write                                                            ▒
                                          - 10.61% ext4_direct_IO                                                                    ▒
                                             - 10.58% ext4_direct_IO_write                                                           ▒
                                                - 9.59% __blockdev_direct_IO                                                         ▒
                                                   - 9.56% do_blockdev_direct_IO                                                     ▒
                                                      - 5.00% do_direct_IO                                                           ▒
                                                         - 1.49% ext4_dio_get_block                                                  ▒
                                                            - 1.49% ext4_get_block_trans                                             ▒
                                                               - 1.42% _ext4_get_block                                               ▒
                                                                  - 1.40% ext4_map_blocks                                            ▒
...
                                                                     - 1.25% ext4_ext_map_blocks                                     ▒
                                                                        - 0.84% ext4_mb_new_blocks                                   ▒
                                                                             0.51% ext4_mb_mark_diskspace_used                       ▒
                                                         - 1.40% iov_iter_get_pages                                                  ▒
                                                            - get_user_pages_fast                                                    ▒
                                                               - 1.38% gup_pgd_range                                                 ▒
                                                                    1.36% gup_pud_range                                              ◆
                                                           0.63% dio_send_cur_page                                                   ▒
                                                      - 2.32% submit_bio                                                             ▒
                                                         - 2.30% generic_make_request                                                ▒
                                                            - 2.16% blk_mq_make_request                                              ▒
                                                               - 1.22% blk_flush_plug_list                                           ▒
                                                                  - 1.21% blk_mq_flush_plug_list                                     ▒
                                                                     - blk_mq_sched_insert_requests                                  ▒
                                                                        - 1.16% blk_mq_run_hw_queue                                  ▒
                                                                           - 1.16% __blk_mq_delay_run_hw_queue                       ▒
                                                                              - __blk_mq_run_hw_queue                                ▒
                                                                                 - blk_mq_sched_dispatch_requests                    ▒
                                                                                    - 1.13% blk_mq_do_dispatch_sched                 ▒
                                                                                       - 1.08% blk_mq_dispatch_rq_list               ▒
                                                                                          - 1.01% scsi_queue_rq                      ▒
                                                                                             - 0.54% print_fmt_kvm_nested_vmrun      ▒
                                                                                                  0.51% megasas_build_and_issue_cmd_f▒
```

As expected, most of the time is spent writing to disk. Note how direct the call stack is: from userland, very briefly through ext4, through the block I/O subsystem, then almost immediately to the disk driver. No work on the page cache or other buffering.

For more information on `bbcp`, see the following references (they are detailed, though only for the original release, and have not been updated for the updates in `gyohng` and `andrew-krasny` forks):

*  [bbcp User Manual](https://www.slac.stanford.edu/~abh/bbcp/) (Stanford)
*  [Using BBCP](http://pcbunn.cithep.caltech.edu/bbcp/using_bbcp.htm) (Caltech)

## Conclusion

For most applications, the bottleneck is I/O to disk or via network. The CPU has such higher bandwidth than these other resources that it's not necessary to even consider CPU overheads or splitting the work across threads to take advantage of multiple cores. But, in some circumstances, with very fast disks and network, the bottleneck may be in a surprising place. Luckily, conventional CPU profiling tools (`top`, `perf`) serve well to identify the bottlenecks and help eliminate them.

In this case, `bbcp` is able to split the work between multiple cores and improve throughput. However, note that it's not always easy to split up the work, and if the work is spread unevenly, you may still have a CPU bottleneck. This is expressed in [Amdahl's law](https://en.wikipedia.org/wiki/Amdahl%27s_law): the speedup is limited the fraction of the original work which can be parallelized effectively.

In a similar spirit, Cloudflare has written on maximizing network throughput ([How to receive a million packets per second](https://blog.cloudflare.com/how-to-receive-a-million-packets/), which also encounters unexpected bottlenecks (like the network card delivering interrupts to a single core).

For more technical content, check out my:

*  [Twitter](https://twitter.com/eigma)
*  [GitHub](https://github.com/cpatulea)
*  [Random collection of old projects](http://vv.carleton.ca/~cat/)
