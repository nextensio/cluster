#
# Author: Davi Gupta (davigupta@gmail.com), Sep 2020
#

all: libaaa.so

libaaa.so: aaa.go
	go build -o $@ -buildmode=c-shared $<

.PHONY: clean
clean:
	-rm libaaa.so libaaa.h
