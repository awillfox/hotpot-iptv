#!/bin/sh
# Generates two small multi-track MKVs + an external srt in testdata/e2e/.
# movie1 carries two audio tracks at different tones so switching between them
# is audible: Thai=440Hz, English=880Hz.
set -e
mkdir -p testdata/e2e
ffmpeg -y -f lavfi -i "testsrc2=duration=30:size=1280x720:rate=25" \
  -f lavfi -i "sine=frequency=440:duration=30" \
  -f lavfi -i "sine=frequency=880:duration=30" \
  -map 0:v -map 1:a -map 2:a \
  -metadata:s:a:0 language=tha -metadata:s:a:1 language=eng \
  -c:v libx264 -preset ultrafast -c:a aac \
  testdata/e2e/movie1.mkv
# movie2 has English audio only, so the rendition union has to fill a silent
# Thai track and empty Thai subtitles for it.
ffmpeg -y -f lavfi -i "testsrc2=duration=30:size=1280x720:rate=25" \
  -f lavfi -i "sine=frequency=660:duration=30" \
  -map 0:v -map 1:a -metadata:s:a:0 language=eng \
  -c:v libx264 -preset ultrafast -c:a aac \
  testdata/e2e/movie2.mkv
cat > testdata/e2e/movie1.tha.srt <<'SRT'
1
00:00:01,000 --> 00:00:05,000
สวัสดี hotpot

2
00:00:10,000 --> 00:00:15,000
บรรทัดที่สอง
SRT
echo "test media written to testdata/e2e/"
