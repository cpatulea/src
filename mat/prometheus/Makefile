export STAGING_DIR = openwrt-sdk-18.06.5-ar71xx-generic_gcc-7.3.0_musl.Linux-x86_64/staging_dir
CC = $(STAGING_DIR)/toolchain-mips_24kc_gcc-7.3.0_musl/bin/mips-openwrt-linux-musl-gcc
CPPFLAGS = -D_GNU_SOURCE
CFLAGS = -W -Wall -std=c11 -O2

all: collect

collect: collect.c
