package main

import (
	"fmt"
	"github.com/Sirupsen/logrus"
	"github.com/mopsalarm/go-pr0gramm"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"
	"os/exec"
	"regexp"
	"sync"
	"flag"

	_ "github.com/lib/pq"
	"database/sql"
	"log"
	"path"
	"errors"
)

type Result struct {
	Item pr0gramm.Item
	File string
	Okay bool
}

type none struct{}

func ConsumeWithChannel(channel chan<- pr0gramm.Item) func(pr0gramm.Item) error {
	return func(item pr0gramm.Item) error {
		channel <- item
		return nil
	}
}

func ItemUrl(item pr0gramm.Item) string {
	return fmt.Sprintf("http://pr0gramm.com/new/%d", item.Id)
}

func ItemLogger(item pr0gramm.Item) logrus.FieldLogger {
	return logrus.WithField("item", ItemUrl(item))
}

func DownloadItemWithCache(item pr0gramm.Item) (string, error) {
	if strings.HasPrefix(item.Image, "//") {
		return "", errors.New("Relative image path looks like an url")
	}

	filename := "cache/" + item.Image
	if st, err := os.Stat(filename); err == nil && st.Size() > 0 {
		return filename, nil
	}

	if err := os.MkdirAll(path.Dir(filename), 0755); err != nil {
		return "", fmt.Errorf("Could not create directory, error: %s", err)
	}

	uri := "http://img.pr0gramm.com/" + item.Image
	response, err := http.DefaultClient.Get(uri)
	if err != nil {
		return "", err
	}

	// discard and close the response
	defer response.Body.Close()
	defer io.Copy(ioutil.Discard, response.Body)

	fp, err := os.OpenFile(filename, os.O_CREATE | os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}

	defer fp.Close()

	// copy response to file
	if _, err = io.Copy(fp, response.Body); err != nil {
		os.Remove(filename)
		return "", err
	}

	return filename, nil
}

func ProcessItem(db *sql.DB, item pr0gramm.Item) {
	if !strings.HasSuffix(item.Image, ".jpg") && !strings.HasSuffix(item.Image, ".png") {
		return
	}

	if CheckItemAlreadyProcessed(db, item.Id) {
		return
	}

	ItemLogger(item).Info("Processing item now")

	filename, err := DownloadItemWithCache(item)
	if err != nil {
		ItemLogger(item).WithError(err).Warn("Could not download item")
		return
	}

	hasText, err := ImageContainsText(filename)

	if err := WriteItemHasText(db, item, hasText); err != nil {
		ItemLogger(item).WithError(err).Warn("Could not write result to database")
	}
}

func ImageContainsText(filename string) (bool, error) {
	command := exec.Command("tesseract", filename, "stdout")
	output, err := command.Output()
	if err != nil {
		return false, nil
	}

	// clean and count chars.
	cleaned := regexp.MustCompile("[^a-z.]").ReplaceAllString(string(output), "")
	return len(cleaned) > 30, nil
}

func CheckItemAlreadyProcessed(db *sql.DB, itemId pr0gramm.Id) bool {
	tx, err := db.Begin()
	if err != nil {
		return false
	}

	defer tx.Commit()

	result, err := tx.Query("SELECT 1 FROM items_text WHERE item_id=$1", itemId)
	if err != nil {
		return false
	}

	defer result.Close()
	return result.Next()
}

func WriteItemHasText(db *sql.DB, item pr0gramm.Item, hasText bool) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}

	defer tx.Commit()

	_, err = tx.Exec("INSERT INTO items_text (item_id, has_text) VALUES ($1, $2) ON CONFLICT DO NOTHING", item.Id, hasText);
	if err != nil {
		return err
	}

	return nil
}

func main() {
	postgresAddress := flag.String("postgres", "host=localhost user=postgres password=password sslmode=disable",
		"Postgres connection string")

	startAtId := flag.Uint64("start-at", 0, "Starts at this id if given.")

	flag.Parse()

	// open database connection
	db, err := sql.Open("postgres", *postgresAddress)
	if err != nil {
		log.Fatal(err)
	}

	// cleanup on close and set default flags.
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(1 * time.Minute)

	// check if connect is valid and available
	if err = db.Ping(); err != nil {
		logrus.Fatal(err)
		return
	}

	request := pr0gramm.NewItemsRequest().WithFlags(pr0gramm.AllContentTypes)

	if *startAtId > 0 {
		request = request.WithOlderThan(pr0gramm.Id(*startAtId))
	}

	inputItems := make(chan pr0gramm.Item, 8)

	go func() {
		defer close(inputItems)

		// read some items into the input channel.
		err := pr0gramm.Stream(request, pr0gramm.ConsumeIf(func(item pr0gramm.Item) bool {
			hoursInYear := float64(12 * (24 * 31))
			return time.Since(item.Created.Time).Hours() < 20 * hoursInYear
		}, ConsumeWithChannel(inputItems)))

		if err != nil {
			logrus.WithError(err).Warn("Could not read feed.")
		}
	}()

	wg := sync.WaitGroup{}

	// start processors
	for idx := 0; idx < 6; idx++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range inputItems {
				ProcessItem(db, item)
			}
		}()
	}

	wg.Wait()
}
