#include <algorithm>
#include <stdio.h>
#include <assert.h>
#include <vector>
#include <string.h>

using namespace std;

char buffer[150000000];
size_t len;

int main() {
  fprintf(stderr, "reading...\n");
  FILE* f = fopen("words.txt", "r");
  assert(f);
  len = fread(buffer, 1, sizeof(buffer)-1, f);
  assert(len != 0);
  buffer[len] = '\0';
  assert(feof(f));
  fclose(f);

  fprintf(stderr, "tokenizing...\n");
  vector<const char*> words;
  char* p = buffer;
  const char* word;
  while ((word = strsep(&p, "\n"))) {
    words.push_back(word);
  }

  fprintf(stderr, "got %lu words\n", words.size());

  fprintf(stderr, "sorting...\n");
  stable_sort(words.begin(), words.end(), [](const char* a, const char* b) {
    return strcmp(a, b) < 0;
  });

  fprintf(stderr, "sorted: ");
  for (int i = 0; i < 10; ++i) {
    fprintf(stderr, "%s ", words[i]);
  }
  fprintf(stderr, "\n");
  return 0;
}