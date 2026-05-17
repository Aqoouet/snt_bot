package render

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	_ "embed"

	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

//go:embed font.ttf
var fontData []byte

var loadedFont *truetype.Font

func init() {
	f, err := freetype.ParseFont(fontData)
	if err != nil {
		panic("render: parse font: " + err.Error())
	}
	loadedFont = f
}

// palette
var (
	colBg        = color.RGBA{R: 0x1e, G: 0x1e, B: 0x2e, A: 0xff}
	colBgAlt     = color.RGBA{R: 0x18, G: 0x18, B: 0x25, A: 0xff}
	colHeader    = color.RGBA{R: 0x31, G: 0x32, B: 0x44, A: 0xff}
	colText      = color.RGBA{R: 0xcd, G: 0xd6, B: 0xf4, A: 0xff}
	colHeaderTxt = color.RGBA{R: 0x89, G: 0xdc, B: 0xeb, A: 0xff}
	colTitle     = color.RGBA{R: 0xcb, G: 0xa6, B: 0xf7, A: 0xff}
	colDivider   = color.RGBA{R: 0x45, G: 0x47, B: 0x5a, A: 0xff}
)

const (
	fontSize   = 14.0
	paddingX   = 16
	paddingY   = 12
	cellPadX   = 10
	cellPadY   = 8
	lineHeight = 22
)

// RenderTable renders a titled table as a PNG and returns the bytes.
func RenderTable(title string, headers []string, rows [][]string) ([]byte, error) {
	face := truetype.NewFace(loadedFont, &truetype.Options{
		Size:    fontSize,
		DPI:     96,
		Hinting: font.HintingFull,
	})
	defer face.Close()

	colWidths := columnWidths(face, headers, rows)

	totalW := paddingX*2 + cellPadX*(len(headers)-1)
	for _, w := range colWidths {
		totalW += w
	}
	// add separator pipes width
	totalW += (len(headers) + 1) * 1

	rowH := lineHeight + cellPadY*2
	titleH := lineHeight + paddingY*2
	headerH := rowH
	totalH := titleH + headerH + rowH*len(rows) + paddingY

	img := image.NewRGBA(image.Rect(0, 0, totalW, totalH))
	fillRect(img, image.Rect(0, 0, totalW, totalH), colBg)

	// title bar
	fillRect(img, image.Rect(0, 0, totalW, titleH), colHeader)
	drawText(img, face, title, paddingX, paddingY+lineHeight, colTitle)

	// divider under title
	fillRect(img, image.Rect(0, titleH, totalW, titleH+1), colDivider)

	// header row
	y := titleH
	fillRect(img, image.Rect(0, y, totalW, y+headerH), colHeader)
	drawRowCells(img, face, headers, colWidths, y, colHeaderTxt)
	y += headerH

	// data rows
	for i, row := range rows {
		bg := colBg
		if i%2 == 1 {
			bg = colBgAlt
		}
		fillRect(img, image.Rect(0, y, totalW, y+rowH), bg)
		drawRowCells(img, face, row, colWidths, y, colText)
		// subtle row divider
		fillRect(img, image.Rect(0, y+rowH-1, totalW, y+rowH), colDivider)
		y += rowH
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func columnWidths(face font.Face, headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = textWidth(face, h)
	}
	for _, row := range rows {
		for i := range headers {
			if i < len(row) {
				if w := textWidth(face, row[i]); w > widths[i] {
					widths[i] = w
				}
			}
		}
	}
	for i := range widths {
		widths[i] += cellPadX * 2
	}
	return widths
}

func drawRowCells(img *image.RGBA, face font.Face, cells []string, colWidths []int, rowY int, clr color.RGBA) {
	x := paddingX
	textY := rowY + cellPadY + lineHeight
	for i, cell := range cells {
		if i >= len(colWidths) {
			break
		}
		// vertical divider
		if i > 0 {
			fillRect(img, image.Rect(x-1, rowY, x, rowY+lineHeight+cellPadY*2), colDivider)
		}
		drawText(img, face, cell, x+cellPadX, textY, clr)
		x += colWidths[i]
	}
}

func textWidth(face font.Face, s string) int {
	var advance fixed.Int26_6
	prev := rune(0)
	for _, r := range s {
		if prev != 0 {
			advance += face.Kern(prev, r)
		}
		a, ok := face.GlyphAdvance(r)
		if ok {
			advance += a
		}
		prev = r
	}
	return int(math.Ceil(float64(advance) / 64.0))
}

func drawText(img *image.RGBA, face font.Face, s string, x, y int, clr color.RGBA) {
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(clr),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(s)
}

func fillRect(img *image.RGBA, r image.Rectangle, clr color.RGBA) {
	for py := r.Min.Y; py < r.Max.Y; py++ {
		for px := r.Min.X; px < r.Max.X; px++ {
			img.SetRGBA(px, py, clr)
		}
	}
}
