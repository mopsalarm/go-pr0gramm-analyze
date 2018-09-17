FROM golang:1.11-stretch as builder

ENV GO111MODULE=on
ENV PACKAGE github.com/mopsalarm/pr0gramm-analyze
WORKDIR $GOPATH/src/$PACKAGE/

COPY go.mod go.sum ./
RUN go mod download

ENV CGO_ENABLED=0

COPY . .
RUN go build -v -ldflags="-s -w" -o /binary .


FROM vimagick/tesseract

RUN apt-get update && apt-get install -y ca-certificates && apt-get clean

VOLUME /cache
COPY --from=builder /binary /go-pr0gramm-analyze

ENTRYPOINT ["/go-pr0gramm-analyze"]
