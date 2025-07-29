# Fast-OCR: Scalable Event-Driven OCR Pipeline for PDFs

This solution provides a serverless pipeline on Google Cloud to extract text
from PDF files. It automatically splits large PDFs into smaller chunks and then
runs Optical Character Recognition (OCR) on Document AI on each chunk to extract
its text content.

## Architecture

The architecture uses Google Cloud Storage, Cloud Run, and Eventarc to create an
event-driven processing flow.

```
[PDF Upload]
      |
      v
+------------------+
| GCS Bucket 1     |  (e.g., raw-pdfs)
| (Raw PDFs)       |
+------------------+
      |
      | 1. Object Finalized Event
      v
+------------------+
| Eventarc Trigger |
+------------------+
      |
      | 2. Invokes Service
      v
+------------------+
| Cloud Run        |
| (chunker)        |
+------------------+
      |
      | 3. Writes Chunks
      v
+------------------+
| GCS Bucket 2     |  (e.g., chunked-pdfs)
| (PDF Chunks)     |
+------------------+
      |
      | 4. Object Finalized Event
      v
+------------------+
| Eventarc Trigger |
+------------------+
      |
      | 5. Invokes Service
      v
+------------------+
| Cloud Run        |
| (extractor)      |
+------------------+
      |
      | 6. Writes Extracted Text
      v
+------------------+
| GCS Bucket 3     |  (e.g., extracted-text)
| (Extracted Text) |
+------------------+

```

**Flow:**

1.  A raw PDF is uploaded to the first GCS bucket.
2.  An Eventarc trigger fires on the "object finalized" event, invoking the
    `chunker` Cloud Run service.
3.  The `chunker` service splits the PDF into smaller files and writes them to
    the second GCS bucket.
4.  Another Eventarc trigger fires for each new chunk, invoking the `extractor`
    Cloud Run service.
5.  The `extractor` service calls the Document AI API to perform OCR on the
    chunk.
6.  The extracted text is saved as a `.txt` file in the third GCS bucket.

## Deployment

Deploy each service to Cloud Run using the `gcloud` CLI from the root directory
of the project.

### Chunker Service

The `chunker` service requires environment variables for the output bucket and
the desired chunk size.

```bash
# Navigate to the chunker directory
cd chunker

# Deploy the service
gcloud run deploy chunker-service \
  --source . \
  --region YOUR_REGION \
  --set-env-vars "OUTPUT_BUCKET=your-chunked-pdf-bucket,CHUNK_SIZE=12"
```

### Extractor Service

The `extractor` service requires environment variables for the Document AI
processor location and ID, and the output bucket.

```bash
# Navigate to the extractor directory
cd extractor

# Deploy the service
gcloud run deploy extractor-service \
  --source . \
  --region YOUR_REGION \
  --set-env-vars "DOCAI_LOCATION=eu,DOCAI_PROCESSOR=your-processor-id,OUTPUT_BUCKET=your-extracted-text-bucket"
```

**Note:** You must create the GCS buckets and configure the Eventarc triggers
separately. The Cloud Run services will need appropriate IAM permissions to read
from their source buckets, write to their destination buckets, and (for the
extractor) invoke the Document AI API.
