#!/bin/sh
# Build cafard from ~/cafard-ui, make a fresh data dir and images. Then
# start.sh runs the server and seed.mjs fills it.
set -e
D=~/cafard-shots
cd ~/${REMOTE_DIR:-cafard} && go build -o $D/cafard ./cmd/cafard
rm -rf $D/data && mkdir -p $D/data $D/img
echo 'shots-password-1' | $D/cafard passwd -data-dir $D/data
cd $D/img
i=0
for t in "Login page" "Login page after fix" "Settings, dark theme" "Firmware update dialog" "Boot splash" "Dashboard, 3 boards"; do
  i=$((i+1))
  c=$(echo "#3b6ea5 #6a4c93 #2a9d8f #e76f51 #264653 #8ab17d" | cut -d' ' -f$i)
  magick -size 960x600 "gradient:$c-#f4f1ea" -fill '#1d1d1d' -font DejaVu-Sans -pointsize 44 -gravity north -annotate +0+60 "$t" \
    -fill '#ffffffcc' -draw "roundrectangle 180,180 780,520 24,24" -fill '#55555588' -draw "rectangle 230,240 730,270" -draw "rectangle 230,300 600,330" -draw "rectangle 230,360 680,390" \
    "shot$i.png"
done
magick -size 1200x800 "gradient:#ccd5ae-#faedcd" -fill '#333' -font DejaVu-Sans -pointsize 60 -gravity center -annotate +0+0 "bench photo: dut1 LEDs" photo.png
ls
