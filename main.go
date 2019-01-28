package main

import (
	"context"
	"github.com/mopsalarm/go-pr0gramm"
	"github.com/pkg/errors"
	"image"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/jessevdk/go-flags"

	_ "image/jpeg"
	_ "image/png"
)

func DownloadItem(item pr0gramm.Item) (string, error) {
	// create target directory
	if err := os.MkdirAll("cache", 0755); err != nil {
		return "", errors.WithMessage(err, "create temporary directory")
	}

	// target file
	filename := "cache/" + regexp.MustCompile("[^A-Za-z0-9.]+").
		ReplaceAllString(item.Image, "_")

	// source url
	response, err := http.DefaultClient.Get("https://img.pr0gramm.com/" + item.Image)
	if err != nil {
		return "", errors.WithMessage(err, "download image")
	}

	// consume rest of body
	defer func() {
		_, _ = io.Copy(ioutil.Discard, response.Body)
		_ = response.Body.Close()
	}()

	// open target file
	fp, err := os.OpenFile(filename, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", errors.WithMessage(err, "open target file")
	}

	defer fp.Close()

	// copy response to file
	if _, err = io.Copy(fp, response.Body); err != nil {
		// delete target file in case of download error
		_ = os.Remove(filename)
		return "", errors.WithMessage(err, "downloading")
	}

	return filename, nil
}

func ProcessItem(session *pr0gramm.Session, item pr0gramm.Item) error {
	logger := log.WithField("item", item.Id)

	logger.Infof("Downloading")

	filename, err := DownloadItem(item)
	if err != nil {
		return errors.WithMessage(err, "download image to temp")
	}

	defer os.Remove(filename)

	var tags []string

	logger.Infof("Detecting text")
	_, hasText, err := ImageContainsText(filename)
	if err != nil {
		return errors.WithMessage(err, "detecting text")
	}

	if hasText {
		tags = append(tags, "text")
	}

	correctGray, err := ImageContainsCorrectGray(filename)
	if err != nil {
		return errors.WithMessage(err, "detect correct gray")
	}

	if correctGray {
		tags = append(tags, "richtiges grau")
	}

	if len(tags) > 0 {
		logger.Infof("Adding tags: %s", strings.Join(tags, ", "))

		err := session.TagsAdd(item.Id, tags)
		if err != nil {
			return errors.WithMessage(err, "add tag to item")
		}
	}

	return nil
}

func ImageContainsCorrectGray(filename string) (bool, error) {
	fp, err := os.Open(filename)
	if err != nil {
		return false, errors.WithMessage(err, "open image file")
	}

	defer fp.Close()

	image, _, err := image.Decode(fp)
	if err != nil {
		return false, errors.WithMessage(err, "decoding image")
	}

	width := image.Bounds().Dx()

	var grayCount int
	for x := 0; x < width; x++ {
		r, g, b, _ := image.At(x, 0).RGBA()

		dR := 0x1616 - int(r)
		dG := 0x1616 - int(g)
		dB := 0x1818 - int(b)

		th := 0x0606

		if dR*dR+dG*dG+dB*dB < th*th {
			grayCount++
		}
	}

	correctGray := float64(grayCount) > float64(width)*0.75
	return correctGray, nil
}

func ImageContainsText(filename string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, "tesseract", filename, "stdout")

	output, err := command.Output()
	if err != nil {
		return "", false, errors.WithMessage(err, "running tesseract")
	}

	// clean and count chars.
	cleaned := regexp.MustCompile("[^a-zA-Z.]").ReplaceAllString(string(output), "")
	return string(output), len(cleaned) > 30, nil
}

type Updater struct {
	Session *pr0gramm.Session
	Latest  pr0gramm.Id
}

func (u *Updater) Update() error {
	items, err := u.Session.GetItems(pr0gramm.NewItemsRequest())
	if err != nil {
		return errors.WithMessage(err, "fetch items")

	}

	// sort ascending by id
	sort.Slice(items.Items, func(i, j int) bool {
		return items.Items[i].Id < items.Items[j].Id
	})

	for _, item := range items.Items {
		if item.Id <= u.Latest {
			continue
		}

		isImage := strings.HasSuffix(strings.ToLower(item.Image), ".jpg") ||
			strings.HasSuffix(strings.ToLower(item.Image), ".jpeg") ||
			strings.HasSuffix(strings.ToLower(item.Image), ".png")

		if !isImage {
			continue
		}

		// mark as processed so we dont process it twice in case of errors
		u.Latest = item.Id

		log.Infof("Checking if item %d https://img.pr0gramm.com/%s has text", item.Id, item.Image)
		if err := ProcessItem(u.Session, item); err != nil {
			log.Warnf("Processing failed: %s", err)
			break
		}
	}

	return nil
}

func main() {
	var args struct {
		Username string `long:"username" description:"Username to use to access pr0gramm"`
		Password string `long:"password" description:"Password to use to access pr0gramm"`
	}

	if _, err := flags.Parse(&args); err != nil {
		os.Exit(1)
	}

	session := pr0gramm.NewSession(http.Client{Timeout: 10 * time.Second})
	if resp, err := session.Login(args.Username, args.Password); err != nil {
		log.WithError(err).Fatal("Could not login")
		return
	} else {
		if !resp.Success {
			log.Fatal("Login failed.")
			return
		}
	}

	up := Updater{Session: session}

	for {
		if err := up.Update(); err != nil {
			log.Warnf("Update loop failed: %s", err)
		}

		log.Infof("Sleeping for a minute")
		time.Sleep(60 * time.Second)
	}
}
