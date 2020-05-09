#!/usr/bin/python3
import random, string
from pymongo import MongoClient
client = MongoClient('localhost', 27017)

db = client.wordsdb
w = db.words

random.seed(1)
for _ in range(1100):
  docs = []
  for _ in range(100000):
    def _words():
      for _ in range(random.randint(10, 40)):
        yield ''.join([random.choice(string.ascii_lowercase) for _ in range(random.randint(4, 12))])
    docs.append({'body': ' '.join(_words())})
  w.insert_many(docs)
