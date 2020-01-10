#include <arpa/inet.h>
#include <asm/types.h>
#include <assert.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <linux/genetlink.h>
#include <linux/if.h>
#include <linux/netlink.h>
#include <linux/nl80211.h>
#include <signal.h>
#include <stdbool.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>

#define printf_format(x)                                                       \
  _Generic((x), \
    int: "%d", \
    unsigned int: "%u", \
    short int: "%hd", \
    short unsigned int: "%hu", \
    long int: "%ld", \
    long long int: "%lld", \
    void *: "%p" \
  )

#define PCHECK_OP(op, val1, val1s, val2, val2s, p)                             \
  do {                                                                         \
    __auto_type _val1 = (val1);                                                \
    __auto_type _val2 = (val2);                                                \
    int _errno;                                                                \
    if ((p)) {                                                                 \
      _errno = errno;                                                          \
    }                                                                          \
    if (!(_val1 op _val2)) {                                                   \
      fprintf(stderr, "%s:%d Check failed: ", __FILE__, __LINE__);             \
      fprintf(stderr, val1s " " #op " " val2s " (");                           \
      fprintf(stderr, printf_format(_val1), _val1);                            \
      fprintf(stderr, " vs ");                                                 \
      fprintf(stderr, printf_format(_val2), _val2);                            \
      fprintf(stderr, ")");                                                    \
      if (p) {                                                                 \
        fprintf(stderr, ": %s", strerror(_errno));                             \
      }                                                                        \
      fprintf(stderr, "\n");                                                   \
      abort();                                                                 \
    }                                                                          \
  } while (0);

#define CHECK_EQ(val1, val2) PCHECK_OP(==, val1, #val1, val2, #val2, 0)
#define CHECK_NE(val1, val2) PCHECK_OP(!=, val1, #val1, val2, #val2, 0)
#define CHECK_LT(val1, val2) PCHECK_OP(<, val1, #val1, val2, #val2, 0)
#define CHECK_GE(val1, val2) PCHECK_OP(>=, val1, #val1, val2, #val2, 0)
#define PCHECK_EQ(val1, val2) PCHECK_OP(==, val1, #val1, val2, #val2, 1)
#define PCHECK_NE(val1, val2) PCHECK_OP(!=, val1, #val1, val2, #val2, 1)
#define PCHECK_GE(val1, val2) PCHECK_OP(>=, val1, #val1, val2, #val2, 1)

#define ARRAY_SIZE(a) (sizeof(a) / sizeof((a)[0]))

#define bprintf(buf, fmt, ...)                                                 \
  do {                                                                         \
    int rc = snprintf(buf, sizeof(buf), fmt, __VA_ARGS__);                     \
    CHECK_GE(rc, 0);                                                           \
    CHECK_LT(rc, (int)sizeof(buf));                                            \
  } while (0)

struct tar {
  char path[100];
  char mode[8];
  char uid[8];
  char gid[8];
  char size[12];
  char mtime[12];
  char checksum[8];
  char link[1];
  char linked[100];
  char ustar[6];
  char ver[2];
  char owner[32];
  char group[32];
  char devmaj[8];
  char devmin[8];
  char prefix[155];
  char padding[12];
};

static_assert(sizeof(struct tar) == 512, "Tar header should be 512 bytes");

off_t file_size = 1024 * 1024;
off_t max_total = 10 * 1024 * 1024;
int period = 2;

void tar_write(FILE *f, const char *t, const char *dir, const char *name,
               void *data, size_t size) {
  struct tar h = {0};
  CHECK_LT(strlen(name), sizeof(h.path));
  strcpy(h.path, name);
  strcpy(h.mode, "0400");
  strcpy(h.uid, "0");
  strcpy(h.gid, "0");
  sprintf(h.size, "%011o", size);
  sprintf(h.mtime, "%011lo", time(NULL));
  strncpy(h.checksum, "        ", sizeof(h.checksum));
  strncpy(h.link, "0", sizeof(h.link));
  strncpy(h.ustar, "ustar", sizeof(h.ustar));
  strncpy(h.ver, "00", sizeof(h.ver));
  bprintf(h.prefix, "%s%s", t, dir);

  const unsigned char *p = (const unsigned char *)&h;
  int cs = 0;
  for (size_t i = 0; i < sizeof(h); ++i) {
    cs += p[i];
  }
  bprintf(h.checksum, "%06o", cs);

  CHECK_EQ(1u, fwrite(&h, sizeof(h), 1, f));
  CHECK_EQ(1u, fwrite(data, size, 1, f));
  if (size % 512) {
    int pad = 512 - (size % 512);
    char zero[512] = {0};
    CHECK_EQ(1u, fwrite(zero, pad, 1, f));
  }
}

bool in_exclude(const char *name, const char *exclude) {
  char xname[100];
  bprintf(xname, " %s ", name);
  return strstr(exclude, xname);
}

void collect_file(FILE *f, const char *t, const char *dir, const char *name) {
  char path[100];
  bprintf(path, "%s/%s", dir, name);

  FILE *file = fopen(path, "r");
  // May have raced, file no longer exists.
  if (file) {
    char data[32768];
    int rc = fread(data, 1, sizeof(data), file);
    CHECK_GE(rc, 0);

    // Ignore ferror.
    CHECK_EQ(0, fclose(file));

    tar_write(f, t, dir, name, data, rc);
  }
}

void collect(FILE *f, const char *t, const char *dir, const char *exclude) {
  DIR *d = opendir(dir);
  if (!d) {
    // Raced, directory no longer exists.
    return;
  }

  struct dirent *ent;
  while ((ent = readdir(d))) {
    if (ent->d_type == DT_REG && !in_exclude(ent->d_name, exclude)) {
      collect_file(f, t, dir, ent->d_name);
    }
  }
  PCHECK_EQ(0, closedir(d));
}

void cycle_debugfs(FILE *f, const char *t) {
  const char dir[] = "/sys/kernel/debug/ieee80211";
  DIR *d = opendir(dir);
  PCHECK_NE(NULL, (void *)d);

  struct dirent *phy;
  while ((phy = readdir(d))) {
    if (phy->d_name[0] != '.') {
      char phydir[100];
      bprintf(phydir, "%s/%s/statistics", dir, phy->d_name);
      collect(f, t, phydir, "");
      bprintf(phydir, "%s/%s/ath9k", dir, phy->d_name);
      collect(f, t, phydir, " regdump ");

      bprintf(phydir, "%s/%s", dir, phy->d_name);
      DIR *dp = opendir(phydir);
      PCHECK_NE(NULL, (void *)dp);

      struct dirent *netdev;
      while ((netdev = readdir(dp))) {
        if (!strncmp(netdev->d_name, "netdev:", 7)) {
          char devdir[100];
          bprintf(devdir, "%s/%s", phydir, netdev->d_name);
          collect(f, t, devdir, "");

          char dir[100];
          bprintf(dir, "%s/%s/stations", phydir, netdev->d_name);
          DIR *ds = opendir(dir);
          // May have raced, dir no longer exists
          if (ds) {
            struct dirent *sta;
            while ((sta = readdir(ds))) {
              if (sta->d_name[0] != '.') {
                char stadir[100];
                bprintf(stadir, "%s/%s", dir, sta->d_name);
                collect(f, t, stadir, " rc_stats_csv driver_buffered_tids ");
              }
            }
            PCHECK_EQ(0, closedir(ds));
          }
        }
      }
      PCHECK_EQ(0, closedir(dp));
    }
  }

  PCHECK_EQ(0, closedir(d));
}

int nlfd;
short nlfamily;

void nl_getfamily() {
  {
    struct {
      struct nlmsghdr nh;
      struct genlmsghdr gm;
      struct nlattr a;
      char buf[sizeof(NL80211_GENL_NAME)];
    } gf;
    gf.nh.nlmsg_len = sizeof(gf);
    gf.nh.nlmsg_type = GENL_ID_CTRL;
    gf.nh.nlmsg_flags = NLM_F_REQUEST;
    gf.nh.nlmsg_seq = 1;
    gf.nh.nlmsg_pid = 0;
    gf.gm.cmd = CTRL_CMD_GETFAMILY;
    gf.gm.version = 0;
    gf.gm.reserved = 0;
    strcpy(gf.buf, NL80211_GENL_NAME);
    gf.a.nla_type = CTRL_ATTR_FAMILY_NAME;
    gf.a.nla_len = sizeof(gf.a) + strlen(gf.buf) + 1;

    struct sockaddr_nl sa = {.nl_family = AF_NETLINK};
    struct iovec iov = {&gf, sizeof(gf)};
    struct msghdr msg = {&sa, sizeof(sa), &iov, 1, NULL, 0, 0};

    ssize_t rc = sendmsg(nlfd, &msg, 0);
    PCHECK_EQ((ssize_t)iov.iov_len, rc);
  }

  {
    struct {
      struct nlmsghdr nh;
      struct genlmsghdr gm;
      char buf[2048];
    } b;
    struct iovec iov = {&b, sizeof(b)};
    struct msghdr msg = {NULL, 0, &iov, 1, NULL, 0, 0};
    ssize_t rc = recvmsg(nlfd, &msg, 0);
    PCHECK_GE(rc, 0);
    CHECK_EQ(0, msg.msg_flags & MSG_TRUNC);
    CHECK_GE(rc, (ssize_t)b.nh.nlmsg_len);
    CHECK_EQ(GENL_ID_CTRL, b.nh.nlmsg_type);
    CHECK_EQ(0, b.nh.nlmsg_flags & NLM_F_MULTI);
    CHECK_EQ(1u, b.nh.nlmsg_seq);

    nlfamily = 0;

    struct nlattr *a = (struct nlattr *)&b.buf;
    while ((char *)a < (char *)&b + b.nh.nlmsg_len) {
      if (a->nla_type == CTRL_ATTR_FAMILY_ID) {
        nlfamily = *(short *)(a + 1);
        break;
      }
      a = (struct nlattr *)((char *)a + NLA_ALIGN(a->nla_len));
    }

    CHECK_NE(0, nlfamily);
    fprintf(stderr, "nl80211 family id: %hd\n", nlfamily);
  }
}

int getif(const char *name) {
  int fdp = socket(AF_UNIX, SOCK_DGRAM, 0);
  PCHECK_GE(fdp, 0);
  struct ifreq ifr;
  CHECK_LT(strlen(name), sizeof(ifr.ifr_name));
  strcpy(ifr.ifr_name, name);
  PCHECK_GE(ioctl(fdp, SIOCGIFINDEX, &ifr), 0);
  PCHECK_GE(close(fdp), 0);
  return ifr.ifr_ifindex;
}

void nl_dump_stations(FILE *f, const char *t, const char *ifname, int ifindex) {
  char dir[100];
  bprintf(dir, "/nl80211/%s/stations", ifname);

  {
    struct {
      struct nlmsghdr nh;
      struct genlmsghdr gm;
      struct nlattr a;
      int ifindex;
    } gf;
    gf.nh.nlmsg_len = sizeof(gf);
    gf.nh.nlmsg_type = nlfamily;
    gf.nh.nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP;
    gf.nh.nlmsg_seq = 2;
    gf.nh.nlmsg_pid = 0;
    gf.gm.cmd = NL80211_CMD_GET_STATION;
    gf.gm.version = 0;
    gf.gm.reserved = 0;
    gf.a.nla_type = NL80211_ATTR_IFINDEX;
    gf.a.nla_len = sizeof(gf.a) + sizeof(gf.ifindex);
    gf.ifindex = ifindex;

    struct sockaddr_nl sa = {.nl_family = AF_NETLINK};
    struct iovec iov = {&gf, sizeof(gf)};
    struct msghdr msg = {&sa, sizeof(sa), &iov, 1, NULL, 0, 0};

    ssize_t rc = sendmsg(nlfd, &msg, 0);
    PCHECK_EQ((ssize_t)iov.iov_len, rc);
  }

  for (int multi = 0;; ++multi) {
    struct {
      struct nlmsghdr nh;
      struct genlmsghdr gm;
      char buf[20480];
    } b;
    struct iovec iov = {&b, sizeof(b)};
    struct msghdr msg = {NULL, 0, &iov, 1, NULL, 0, 0};
    ssize_t rc = recvmsg(nlfd, &msg, 0);
    PCHECK_GE(rc, 0);
    CHECK_EQ(0, msg.msg_flags & MSG_TRUNC);
    CHECK_GE(rc, (ssize_t)b.nh.nlmsg_len);
    CHECK_NE(0, b.nh.nlmsg_flags & NLM_F_MULTI);
    CHECK_EQ(2u, b.nh.nlmsg_seq);
    if (b.nh.nlmsg_type == NLMSG_DONE) {
      break;
    }
    CHECK_EQ(nlfamily, b.nh.nlmsg_type);

    fprintf(stderr, "stations got %d bytes\n", rc);
    char name[10];
    bprintf(name, "%04d", multi);
    tar_write(f, t, dir, name, &b, rc);
  }
}

bool strsuffix(const char *haystack, const char *needle) {
  size_t hl = strlen(haystack);
  size_t nl = strlen(needle);
  return hl >= nl && !strcmp(haystack + hl - nl, needle);
}

int is_tar_gz(const struct dirent *ent) {
  return strsuffix(ent->d_name, ".tar.gz");
}

void gc() {
  fprintf(stderr, "gc\n");

  struct dirent **tars;
  int count = scandir("/tmp/prom", &tars, &is_tar_gz, &alphasort);
  PCHECK_GE(count, 0);

  off_t size = 0;
  for (int i = count - 1; i >= 0; --i) {
    char path[100];
    bprintf(path, "/tmp/prom/%s", tars[i]->d_name);

    struct stat st;
    PCHECK_EQ(0, lstat(path, &st));
    size += st.st_size;

    fprintf(stderr, "size: %lld\n", st.st_size);
    if (size >= max_total) {
      fprintf(stderr, "delete %s\n", tars[i]->d_name);
      PCHECK_EQ(0, unlink(path));
    }
  }

  for (int i = 0; i < count; ++i) {
    free(tars[i]);
  }
  free(tars);
}

void collectloop() {
  struct timeval tv;
  PCHECK_EQ(0, gettimeofday(&tv, NULL));

  for (;;) {
    char t[17];
    bprintf(t, "%09ld%06ld", tv.tv_sec, tv.tv_usec);

    char zf[100];
    bprintf(zf, "/tmp/prom/%s.tar.gz", t);

    fprintf(stderr, "open new file %s\n", zf);

    // Simplifies lstat() below.
    int fd = open(zf, O_CREAT | O_WRONLY | O_TRUNC, 0644);
    PCHECK_GE(fd, 0);
    PCHECK_EQ(0, close(fd));

    char cmd[100];
    bprintf(cmd, "exec /bin/gzip >%s", zf);
    FILE *f = popen(cmd, "w");
    PCHECK_NE(NULL, (void *)f);

    bool full;
    do {
      bprintf(t, "%09ld%06ld", tv.tv_sec, tv.tv_usec);
      cycle_debugfs(f, t);

      collect_file(f, t, "/proc/net", "nf_conntrack");

      int ifindex = getif("wlan0");
      CHECK_NE(0, ifindex);
      nl_dump_stations(f, t, "wlan0", ifindex);

      PCHECK_EQ(0, fflush(f));

      struct stat st;
      PCHECK_EQ(0, lstat(zf, &st));
      full = st.st_size >= file_size;
      if (full) {
        gc();
      }

      struct timeval tv1;
      PCHECK_EQ(0, gettimeofday(&tv1, NULL));

      float dt = (float)(tv1.tv_sec - tv.tv_sec) +
                 (float)(tv1.tv_usec - tv.tv_usec) / 1000000.;
      fprintf(stderr, "collect time %.01f\n", dt);

      // Wait for timer.
      sigset_t set;
      PCHECK_EQ(0, sigemptyset(&set));
      PCHECK_EQ(0, sigaddset(&set, SIGALRM));

      int sig;
      CHECK_EQ(0, sigwait(&set, &sig));
      CHECK_EQ(SIGALRM, sig);

      PCHECK_EQ(0, gettimeofday(&tv, NULL));
    } while (!full);

    char zero[512] = {0};
    CHECK_EQ(1u, fwrite(zero, sizeof(zero), 1, f));
    CHECK_EQ(0, pclose(f));
  }
}

int main(int argc, char **argv) {
  int opt;

  while ((opt = getopt(argc, argv, "C:T:d:")) != -1) {
    switch (opt) {
    case 'C':
      file_size = strtol(optarg, NULL, 10);
      CHECK_NE(LONG_MIN, file_size);
      CHECK_NE(LONG_MAX, file_size);
      CHECK_GE(file_size, 0);
      break;
    case 'T':
      max_total = strtol(optarg, NULL, 10);
      CHECK_NE(LONG_MIN, max_total);
      CHECK_NE(LONG_MAX, max_total);
      CHECK_GE(max_total, 0);
      break;
    case 'd':
      period = strtol(optarg, NULL, 10);
      CHECK_NE(LONG_MIN, period);
      CHECK_NE(LONG_MAX, period);
      CHECK_GE(period, 0);
      break;
    default:
      fprintf(stderr, "unknown option: %c\n", opt);
      return 1;
    }
  }

  nlfd = socket(AF_NETLINK, SOCK_RAW, NETLINK_GENERIC);
  PCHECK_GE(nlfd, 0);

  nl_getfamily();

  sigset_t set;
  PCHECK_EQ(0, sigemptyset(&set));
  PCHECK_EQ(0, sigaddset(&set, SIGALRM));
  PCHECK_EQ(0, sigprocmask(SIG_BLOCK, &set, NULL));

  struct itimerval timer = {
      .it_value = {.tv_sec = period, .tv_usec = 0},
      .it_interval = {.tv_sec = period, .tv_usec = 0},
  };
  PCHECK_EQ(0, setitimer(ITIMER_REAL, &timer, NULL));

  collectloop();
  return 0;
}
