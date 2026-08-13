FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO off so the binary runs on the ffmpeg image below without glibc coupling.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags='-s -w' -o /out/hotpot .

# FFmpeg with NVENC support; the jrottenberg images bundle nvidia-capable ffmpeg.
FROM jrottenberg/ffmpeg:7.1-nvidia2404
# The base image sets ffmpeg as the entrypoint; clear it so CMD is our binary.
ENTRYPOINT []
COPY --from=build /out/hotpot /usr/local/bin/hotpot
ENV FFMPEG_PATH=/usr/local/bin/ffmpeg \
    FFPROBE_PATH=/usr/local/bin/ffprobe \
    MEDIA_PATH=/media \
    STREAMS_PATH=/streams
EXPOSE 8080
CMD ["hotpot"]
