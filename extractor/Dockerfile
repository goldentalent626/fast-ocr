# Copyright 2025 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM golang:1.24.2-bookworm as builder
WORKDIR /app/
COPY go.* /app/
RUN go mod download
COPY . /app/
RUN CGO_ENABLED=0 go build -v -o server

FROM gcr.io/distroless/static@sha256:72924583773eeeb9a6200e9f6dbfd95a27fbf25d39bfe7062c46d2654628f007
COPY --from=builder /app/server /app/server
CMD ["/app/server"] 
