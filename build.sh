#!/bin/sh
set -e

glide install

CGO_ENABLED=0 go build -a

docker build -t mopsalarm/go-pr0gramm-analyze .
docker push mopsalarm/go-pr0gramm-analyze
