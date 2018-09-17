#!/bin/sh
set -e

docker build -t mopsalarm/go-pr0gramm-analyze .
docker push mopsalarm/go-pr0gramm-analyze
