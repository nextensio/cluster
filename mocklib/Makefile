#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

all: libauth.so

libauth.so: auth.go
	go build -o $@ -buildmode=c-shared $<

.PHONY: clean
clean:
	-rm libauth.so libauth.h
