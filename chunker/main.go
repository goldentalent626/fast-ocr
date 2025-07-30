// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// package main is the entry point for the PDF chunking service.
// This service is designed to be deployed as a Cloud Run service.
// It listens for CloudEvents from Google Cloud Storage, specifically for new PDF files.
// When a PDF is uploaded, this function splits it into smaller PDF files (chunks)
// of a specified number of pages and uploads them to a designated output bucket.
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

// eventHandler is the main HTTP handler for processing CloudEvents.
// It receives an event, parses it to get the details of the uploaded GCS object,
// and then orchestrates the PDF chunking process.
func eventHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Parse the incoming HTTP request into a CloudEvent.
	ce, err := cloudevents.NewEventFromHTTPRequest(r)
	if err != nil {
		log.Printf("failed to parse CloudEvent: %v", err)
		http.Error(w, "Bad Request: expected CloudEvent", http.StatusBadRequest)
		return
	}

	// Unmarshal the CloudEvent data into a StorageObjectData struct.
	// This provides typed access to the event's payload, such as bucket and object names.
	var so storagedata.StorageObjectData
	options := protojson.UnmarshalOptions{
		DiscardUnknown: true, // Be lenient with unknown fields in the event payload.
	}
	err = options.Unmarshal(ce.Data(), &so)
	if err != nil {
		log.Printf("failed to unmarshal: %v", err)
		http.Error(w, "Bad Request: expected Cloud Storage event", http.StatusBadRequest)
		return
	}

	// Extract bucket and object name from the event data.
	bucket := so.GetBucket()
	object := so.GetName()

	// Basic validation to ensure we are processing a PDF file.
	extension := path.Ext(object)

	mimeType := "application/pdf" // Default MIME type.

	switch extension {
	case ".pdf":
		mimeType = "application/pdf"
	default:
		// If the file is not a PDF, log the issue and return an error.
		log.Println("encountered unsupported file type")
		http.Error(w, "encountered unsupported file type", http.StatusUnsupportedMediaType)
		return
	}

	fmt.Printf("Triggering for object '%s' (%s) in bucket '%s'.\n", object, mimeType, bucket)

	// Load configuration from environment variables.
	outputBucket := os.Getenv("OUTPUT_BUCKET")
	chunkSizeRaw := os.Getenv("CHUNK_SIZE")

	// Convert chunk size from string to integer.
	chunkSize, err := strconv.Atoi(chunkSizeRaw)
	if err != nil {
		log.Printf("failed to read chunk size config: %v", err)
		http.Error(w, "error reading chunk size config", http.StatusInternalServerError)
		return
	}

	// Initialize the Google Cloud Storage client.
	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Printf("failed to create GCS client: %v", err)
		http.Error(w, "error creating Storage client", http.StatusInternalServerError)
		return
	}

	// Create a reader for the source GCS object.
	reader, err := gcsClient.Bucket(bucket).Object(object).NewReader(ctx)
	if err != nil {
		log.Printf("failed to open GCS object reader: %v", err)
		http.Error(w, "error opening GCS object reader", http.StatusInternalServerError)
		return
	}
	defer reader.Close()

	// Read the entire PDF into memory. This is required because the pdfcpu library
	// needs an io.ReadSeeker, and the GCS reader is only an io.Reader.
	// This also allows for safe concurrent processing from multiple goroutines,
	// as each will get its own reader on this byte slice.
	blobBytes, err := io.ReadAll(reader)
	if err != nil {
		log.Printf("failed to read GCS object: %v", err)
		http.Error(w, "error reading GCS object", http.StatusInternalServerError)
		return
	}

	// Create a new reader that implements io.ReadSeeker from the in-memory byte slice.
	readSeeker := bytes.NewReader(blobBytes)

	// Get the total number of pages in the PDF to calculate chunking.
	pageCount, err := pdfcpu.PageCount(readSeeker, nil)
	if err != nil {
		log.Printf("failed to inspect PDF object: %v", err)
		http.Error(w, "error inspecting PDF object", http.StatusInternalServerError)
		return
	}

	// Calculate how many chunks will be created. Note: this integer division
	// truncates, so any remaining pages will be ignored.
	chunkCount := pageCount / chunkSize
	// Get the base name of the file, without the extension, to use for chunk naming.
	basename := strings.TrimSuffix(object, extension)

	fmt.Printf(
		"Processing object '%s' (%s) with %d pages, creating %d chunks of %d pages.\n",
		object,
		mimeType,
		pageCount,
		chunkCount,
		chunkSize,
	)

	// Use a WaitGroup to process chunks concurrently and wait for all to complete.
	var wg sync.WaitGroup

	// Loop through the number of chunks to create.
	wg.Add(chunkCount) // Preallocate waitgroup
	for chunkIndex := range chunkCount {
		// Launch a new goroutine to process the chunk.
		go func(chunkIndex int) {
			defer wg.Done() // Decrement the counter when the goroutine completes.
			// Use local read seeker to avoid race conditions with the seek pointer
			localReadSeeker := bytes.NewReader(blobBytes)
			// Construct the name for the output chunk file.
			chunkedName := fmt.Sprintf("%s_chunk_%06d%s", basename, chunkIndex, extension)

			// Create a writer for the new chunk object in the output bucket.
			writer := gcsClient.Bucket(outputBucket).Object(chunkedName).NewWriter(ctx)

			// Define the page range for this specific chunk.
			// Note: pdfcpu page selection is 1-based, but this code uses 0-based logic,
			// which may lead to unexpected page ranges.
			pageRange := []string{
				fmt.Sprintf("%d-%d", (chunkIndex*chunkSize)+1, ((chunkIndex+1)*chunkSize)+1),
			}

			// Use pdfcpu.Trim to extract the page range from the source PDF and write it to the GCS writer.
			pdfcpu.Trim(localReadSeeker, writer, pageRange, nil)

			// Close the writer to finalize the upload.
			err = writer.Close()
			if err != nil {
				fmt.Printf("error writing to GCS for object %s: %v.\n", chunkedName, err)
			}

			fmt.Printf("Wrote chunk %s to bucket %s.\n", chunkedName, outputBucket)
		}(chunkIndex)
	}
	// Wait for all goroutines in the WaitGroup to finish.
	wg.Wait()

	// Close the GCS client to release resources.
	gcsClient.Close()

	fmt.Print("Execution complete.\n")

	// Send a success response.
	w.Write([]byte("ok"))
}

// main is the entry point of the application.
// It sets up an HTTP server to listen for incoming requests, which is the
// standard way to run a service for Google Cloud Functions or Cloud Run.
func main() {
	// The eventHandler function is registered to handle all incoming HTTP requests.
	http.HandleFunc("/", eventHandler)
	// Determine port for HTTP service.
	// The PORT environment variable is a standard convention for cloud-based services.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Default to 8080 if PORT is not set.
	}
	// Start HTTP server.
	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		// If the server fails to start, log the error and exit.
		log.Fatal(err)
	}
}
