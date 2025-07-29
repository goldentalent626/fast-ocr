package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

	"cloud.google.com/go/storage"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/googleapis/google-cloudevents-go/cloud/storagedata"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/api"
	"google.golang.org/protobuf/encoding/protojson"
)

func eventHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ce, err := cloudevents.NewEventFromHTTPRequest(r)
	if err != nil {
		log.Printf("failed to parse CloudEvent: %v", err)
		http.Error(w, "Bad Request: expected CloudEvent", http.StatusBadRequest)
		return
	}

	var so storagedata.StorageObjectData
	options := protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
	err = options.Unmarshal(ce.Data(), &so)
	if err != nil {
		log.Printf("failed to unmarshal: %v", err)
		http.Error(w, "Bad Request: expected Cloud Storage event", http.StatusBadRequest)
		return
	}

	bucket := so.GetBucket()
	object := so.GetName()

	extension := path.Ext(object)

	mimeType := "application/pdf"

	switch extension {
	case ".pdf":
		mimeType = "application/pdf"
	default:
		log.Println("encountered unsupported file type")
		http.Error(w, "encountered unsupported file type", http.StatusBadRequest)
		return
	}

	fmt.Printf("Triggering for object '%s' (%s) in bucket '%s'.\n", object, mimeType, bucket)

	outputBucket := os.Getenv("OUTPUT_BUCKET")
	chunkSizeRaw := os.Getenv("CHUNK_SIZE")

	chunkSize, err := strconv.Atoi(chunkSizeRaw)
	if err != nil {
		log.Printf("failed to read chunk size config: %v", err)
		http.Error(w, "error reading chunk size config", http.StatusBadRequest)
		return
	}

	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Printf("failed to create GCS client: %v", err)
		http.Error(w, "error creating Storage client", http.StatusBadRequest)
		return
	}

	reader, err := gcsClient.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		log.Printf("failed to open GCS object reader: %v", err)
		http.Error(w, "error opening GCS object reader", http.StatusBadRequest)
		return
	}
	defer reader.Close()

	blobBytes, err := io.ReadAll(reader)
	if err != nil {
		log.Printf("failed to read GCS object: %v", err)
		http.Error(w, "error reading GCS object", http.StatusBadRequest)
		return
	}

	readSeeker := bytes.NewReader(blobBytes)

	pageCount, err := pdfcpu.PageCount(readSeeker, nil)
	if err != nil {
		log.Printf("failed to inspect PDF object: %v", err)
		http.Error(w, "error inspecting PDF object", http.StatusBadRequest)
		return
	}

	chunkCount := pageCount / chunkSize
	basename := strings.TrimSuffix(object, extension)

	fmt.Printf(
		"Processing object '%s' (%s) with %d pages, creating %d chunks of %d pages.\n",
		object,
		mimeType,
		pageCount,
		chunkCount,
		chunkSize,
	)

	var wg sync.WaitGroup

	for chunkIndex := range chunkCount {
		wg.Add(1)
		go func(chunkIndex int) {
			defer wg.Done()
			chunkedName := fmt.Sprintf("%s_chunk_%06d%s", basename, chunkIndex, extension)

			writer := gcsClient.Bucket(outputBucket).Object(chunkedName).NewWriter(ctx)

			pageRange := []string{
				fmt.Sprintf("%d-%d", chunkIndex*chunkSize, (chunkIndex+1)*chunkSize),
			}

			pdfcpu.Trim(readSeeker, writer, pageRange, nil)

			writer.Close()

			fmt.Printf("Wrote chunk %s to bucket %s.\n", chunkedName, outputBucket)
		}(chunkIndex)
	}
	wg.Wait()

	gcsClient.Close()

	fmt.Print("Execution complete.\n")

	w.Write([]byte("ok"))
}

func main() {
	http.HandleFunc("/", eventHandler)
	// Determine port for HTTP service.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	// Start HTTP server.
	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
