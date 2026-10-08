# Cascade data

`facefinder` is the frontal-face LBP cascade classifier used by
`PigoFaceProvider` (internal/ml/faces.go). It is distributed with
[esimov/pigo](https://github.com/esimov/pigo) (MIT-licensed library) and is
derived from the OpenCV LBP frontal-face cascade (Apache-2.0).

- Source: https://github.com/esimov/pigo/tree/master/cascade
- pigo license: MIT (see github.com/esimov/pigo LICENSE)
- Cascade data license: Apache-2.0 (OpenCV LBP cascade)

It is embedded into the Cairn binary via `go:embed` so face detection works
with no external model files at runtime.