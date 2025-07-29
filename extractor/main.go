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
	basename := strings.TrimSuffix(object, extension)
	extractedName := fmt.Sprintf("%s_extracted.txt", basename)

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

	project := os.Getenv("GOOGLE_CLOUD_PROJECT")
	location := os.Getenv("DOCAI_LOCATION")
	processor := os.Getenv("DOCAI_PROCESSOR")
	outputBucket := os.Getenv("OUTPUT_BUCKET")

	endpoint := fmt.Sprintf("%s-documentai.googleapis.com:443", location)
	docaiClient, err := documentai.NewDocumentProcessorClient(ctx, option.WithEndpoint(endpoint))
	if err != nil {
		log.Printf("failed to create docai client: %v", err)
		http.Error(w, "error creating Document AI client", http.StatusBadRequest)
		return
	}
	defer docaiClient.Close()

	req := &documentaipb.ProcessRequest{
		Name: fmt.Sprintf("projects/%s/locations/%s/processors/%s", project, location, processor),
		Source: &documentaipb.ProcessRequest_GcsDocument{
			GcsDocument: &documentaipb.GcsDocument{
				GcsUri:   fmt.Sprintf("gs://%s/%s", bucket, object),
				MimeType: mimeType,
			},
		},
	}
	resp, err := docaiClient.ProcessDocument(ctx, req)
	if err != nil {
		log.Printf("failed to process document: %v", err)
		http.Error(w, "failedd to process document", http.StatusBadRequest)
		return
	}

	extracted_text := resp.Document.Text

	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		log.Printf("failed to create GCS client: %v", err)
		http.Error(w, "error creating Storage client", http.StatusBadRequest)
		return
	}

	writer := gcsClient.Bucket(outputBucket).Object(extractedName).NewWriter(ctx)
	byteCount, err := io.WriteString(writer, extracted_text)
	if err != nil {
		log.Printf("failed to write GCS object: %v", err)
		http.Error(w, "error writing extracted text", http.StatusBadRequest)
		return
	}
	writer.Close()
	gcsClient.Close()

	fmt.Printf(
		"Extracted %d bytes and wrote to object %s in bucket %s.\n",
		byteCount,
		extractedName,
		outputBucket,
	)

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
