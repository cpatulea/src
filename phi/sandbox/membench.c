#include <stdio.h>
#include <sys/time.h>
#include <assert.h>
#include <fcntl.h>
#include <sys/mman.h>
#include <unistd.h>
#include <signal.h>
#include <assert.h>
#include <stdlib.h>
#include <time.h>

void segv(int s) {
  fprintf(stderr, "SIGSEGV!\n");
  abort();
}

long micros() {
  struct timespec t;
  clock_gettime(CLOCK_REALTIME, &t);
  return (long)t.tv_sec*1000000 + (long)t.tv_nsec/1000;
}

int main() {
  signal(SIGSEGV, &segv);
  setvbuf(stdout, NULL, _IONBF, 0);

  char cacheline[64] __attribute__((aligned(64)));
  *(void **)&cacheline[0] = &cacheline[0];

  for (int trial = 0; trial < 3; trial += 1) {
    long start = micros(), deadline = start + 100000, end;
    void **p = (void **)&cacheline[0];
    size_t batches = 0;
    const size_t kIter = 1000000;
    while ((end = micros()) < deadline) {
      for (size_t i = 0; i < kIter;) {
#define ONE \
          if (1) asm volatile ( \
            "CLEVICT0 %0\n" \
            "CLEVICT1 %0\n" \
            : : "m"(p)); \
          p = (void **)*p; \
          ++i;
        ONE;
      }
      ++batches;
    }
    printf("", p);
    printf("%8d] %6.02f ns (over %ld batch, %ld iter)\n",
      trial,
      (double)(end-start)/(batches*kIter)*1e3,
      batches, batches*kIter);
  }
  return 0;
}
