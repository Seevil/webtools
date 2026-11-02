package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

func main() {
	if len(os.Args) != 3 {
		fatal(fmt.Errorf("usage: iconbuild <mark.svg> <output-directory>"))
	}
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		fatal(err)
	}
	mark, err := ui.ParseSVG(data)
	if err != nil {
		fatal(err)
	}
	out := os.Args[2]
	if err := os.MkdirAll(out, 0755); err != nil {
		fatal(err)
	}
	sizes := []int{16, 32, 48, 64, 128, 256}
	images := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		img := renderTransparent(mark, size)
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			fatal(err)
		}
		images = append(images, buf.Bytes())
	}
	if err := writeIfChanged(filepath.Join(out, "app.png"), images[len(images)-1]); err != nil {
		fatal(err)
	}
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, uint16(0))
	binary.Write(&ico, binary.LittleEndian, uint16(1))
	binary.Write(&ico, binary.LittleEndian, uint16(len(sizes)))
	offset := 6 + len(sizes)*16
	for i, size := range sizes {
		width := byte(size)
		ico.Write([]byte{width, width, 0, 0})
		binary.Write(&ico, binary.LittleEndian, uint16(1))
		binary.Write(&ico, binary.LittleEndian, uint16(32))
		binary.Write(&ico, binary.LittleEndian, uint32(len(images[i])))
		binary.Write(&ico, binary.LittleEndian, uint32(offset))
		offset += len(images[i])
	}
	for _, data := range images {
		ico.Write(data)
	}
	if err := writeIfChanged(filepath.Join(out, "app.ico"), ico.Bytes()); err != nil {
		fatal(err)
	}
}

func renderTransparent(mark *ui.SVG, size int) *image.NRGBA {
	render := func(background color.NRGBA) image.Image {
		return ui.Render(func(c *ui.Context) {
			ui.Column(c).Fill().Background(ui.RGB(background.R, background.G, background.B)).Children(func() {
				ui.Image(c, mark).Width(float32(size)).Height(float32(size))
			})
		}, size, size, 1)
	}
	black := render(color.NRGBA{A: 255})
	white := render(color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	out := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			b := color.NRGBAModel.Convert(black.At(x, y)).(color.NRGBA)
			w := color.NRGBAModel.Convert(white.At(x, y)).(color.NRGBA)
			alpha := 255 - (int(w.R)-int(b.R)+int(w.G)-int(b.G)+int(w.B)-int(b.B)+1)/3
			if alpha < 0 {
				alpha = 0
			} else if alpha > 255 {
				alpha = 255
			}
			if alpha == 0 {
				continue
			}
			a := uint8(alpha)
			unblend := func(v uint8) uint8 {
				n := (int(v)*255 + alpha/2) / alpha
				if n > 255 {
					return 255
				}
				return uint8(n)
			}
			out.SetNRGBA(x, y, color.NRGBA{R: unblend(b.R), G: unblend(b.G), B: unblend(b.B), A: a})
		}
	}
	return out
}

func writeIfChanged(path string, data []byte) error {
	old, err := os.ReadFile(path)
	if err == nil && bytes.Equal(old, data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
