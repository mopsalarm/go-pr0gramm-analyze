FROM vimagick/tesseract

VOLUME /cache
COPY go-pr0gramm-analyze /

ENTRYPOINT ["/go-pr0gramm-analyze"]
