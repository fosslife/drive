# The container runs the same single binary as everything else: the image is a
# delivery mechanism, not a second way to run the product. Nothing is installed
# beside it — no runtime, no database, no web server.
#
#   podman build -t drive .
#   podman run -v ./data:/data -p 8080:8080 drive
#
# The interface is built here rather than assumed, because `go build` does not
# run it and an image missing web/dist would serve an API and an apology.
FROM docker.io/library/node:22-alpine AS web
WORKDIR /src
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM docker.io/library/golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/dist ./web/dist
ARG VERSION=dev
# CGO stays off: modernc.org/sqlite is pure Go, so the result is one static
# file that runs on scratch and cross-compiles without a C toolchain.
RUN CGO_ENABLED=0 go build -ldflags "-X main.Version=${VERSION}" -o /drive ./cmd/drive

FROM scratch
# The CA roots are the one thing the binary cannot carry: without them the ACME
# client cannot verify Let's Encrypt, so DRIVE_HOSTNAME would fail at startup.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /drive /drive
# Everything that survives a restart lives here, which is why it is the only
# mount: index, files, trash, thumbnails, certificates.
ENV DRIVE_DATA_DIR=/data
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/drive"]
