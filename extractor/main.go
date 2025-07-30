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

// package main is the entry point for the document text extraction service.
// This service is designed to be deployed as a Cloud Run service.
// It listens for CloudEvents from Google Cloud Storage, specifically for new PDF files.
// When a PDF is uploaded, this function uses the Google Cloud Document AI API
// to perform Optical Character Recognition (OCR) and extract the text content.
// The extracted text is then saved to a new .txt file in a designated output bucket.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"

	documentai "cloud.google.com/go/documentai/apiv1"
	"cloud.google.com/go/documentai/apiv1/documentaipb"
	"cloud.google.com/go/storage"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/googleapis/google-cloudevents-go/cloud/storagedata"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/encoding/protojson"
)

// eventHandler is the main HTTP handler for processing CloudEvents.
// It receives an event, parses it to get the details of the uploaded GCS object,
// and then orchestrates the text extraction process using Document AI.
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

	// Determine the output filename for the extracted text.
	extension := path.Ext(object)
	basename := strings.TrimSuffix(object, extension)
	extractedName := fmt.Sprintf("%s_extracted.txt", basename)

	// Basic validation to ensure we are processing a supported file type.
	mimeType := "application/pdf" // Default MIME type.

	switch extension {
	case ".pdf":
		mimeType = "application/pdf"
	default:
		// If the file is not a PDF, log the issue and return an error.
		log.Println("encountered unsupported file type")
		http.Error(w, "encountered unsupported file type",
			http.StatusUnsupportedMediaType)
		return
	}

	fmt.Printf("Triggering for object '%s' (%s) in bucket '%s'.\n", object, mimeType, bucket)

	// Load configuration from environment variables.
	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	location := os.Getenv("DOCAI_LOCATION")   // e.g., "us" or "eu"
	processor := os.Getenv("DOCAI_PROCESSOR") // The ID of the Document AI processor
	outputBucket := os.Getenv("OUTPUT_BUCKET")

	// The Document AI client can be forced to use European endpoints.
	endpoint := fmt.Sprintf("%s-documentai.googleapis.com:443", location)
	// Initialize the Document AI client with the regional endpoint.
	docaiClient, err := documentai.NewDocumentProcessorClient(ctx, option.WithEndpoint(endpoint))
	if err != nil {
		log.Printf("failed to create docai client: %v", err)
		http.Error(w, "error creating Document AI client",
			http.StatusInternalServerError)
		return
	}
	defer docaiClient.Close()

	// Construct the Document AI processing request.
	req := &documentaipb.ProcessRequest{
		// The full resource name of the processor.
		Name: fmt.Sprintf("projects/%s/locations/%s/processors/%s", project, location, processor),
		// Specify the source of the document to process.
		Source: &documentaipb.ProcessRequest_GcsDocument{
			GcsDocument: &documentaipb.GcsDocument{
				GcsUri:   fmt.Sprintf("gs://%s/%s", bucket, object),
				MimeType: mimeType,
			},
		},
		ImagelessMode: true,
	}
	// Call the Document AI API to process the document.
	resp, err := docaiClient.ProcessDocument(ctx, req)
	if err != nil {
		log.Printf("failed to process document: %v", err)
		http.Error(w, "failedd to process document", http.StatusInternalServerError)
		return
	}

	// Extract the full text content from the processor's response.
	extracted_text := resp.Document.Text

	// Initialize the Google Cloud Storage client to write the output.
	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Printf("failed to create GCS client: %v", err)
		http.Error(w, "error creating Storage client",
			http.StatusInternalServerError)
		return
	}

	// Create a writer for the new text file object in the output bucket.
	writer := gcsClient.Bucket(outputBucket).Object(extractedName).NewWriter(ctx)
	// Write the extracted text to the GCS object.
	byteCount, err := io.WriteString(writer, extracted_text)
	if err != nil {
		log.Printf("failed to write GCS object: %v", err)
		http.Error(w, "error writing extracted text", http.StatusInternalServerError)
		return
	}
	// Close the writer to finalize the upload.
	writer.Close()
	// Close the GCS client to release resources.
	gcsClient.Close()

	fmt.Printf(
		"Extracted %d bytes and wrote to object %s in bucket %s.\n",
		byteCount,
		extractedName,
		outputBucket,
	)

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
