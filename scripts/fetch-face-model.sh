#!/bin/sh
# Download the face-recognition model Cairn uses for people matching.
#
# The file is InsightFace's MobileFaceNet (buffalo_s, 13 MB) as redistributed
# by the Immich project. InsightFace's pretrained models are licensed for
# NON-COMMERCIAL research use; check that suits you before using it. Any
# ArcFace-style ONNX model with 112x112 input and a 512-value output also
# works: put it at the path below or point CAIRN_ML_FACE_MODEL at it.
set -eu
dir="${CAIRN_DATA_DIR:-./cairn-data}/models"
dest="${CAIRN_ML_FACE_MODEL:-$dir/face-recognition.onnx}"
url="https://huggingface.co/immich-app/buffalo_s/resolve/main/recognition/model.onnx"
mkdir -p "$(dirname "$dest")"
curl -fL --retry 3 -o "$dest.part" "$url"
mv "$dest.part" "$dest"
echo "Saved $dest. Restart Cairn; existing faces are re-scanned with the new model."
