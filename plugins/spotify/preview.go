package spotify

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"

	"github.com/disintegration/imaging"
	"github.com/fogleman/gg"
	"github.com/golang/freetype/truetype"
	"github.com/lucasb-eyer/go-colorful"
)

//go:embed fira.ttf
var firaFontData []byte

const (
	previewWidth  = 600
	previewHeight = 800
	albumArtSize  = 400
	marginTop     = 100
	textSpacing   = 30
)

func extractDominantColors(img image.Image) []color.Color {
	bounds := img.Bounds()
	width, height := bounds.Max.X, bounds.Max.Y
	
	colorMap := make(map[uint32]int)
	
	step := 10
	for y := 0; y < height; y += step {
		for x := 0; x < width; x += step {
			c := img.At(x, y)
			r, g, b, _ := c.RGBA()
			key := (r/256)<<16 | (g/256)<<8 | (b/256)
			colorMap[key]++
		}
	}
	
	type colorCount struct {
		color color.Color
		count int
	}
	
	var colors []colorCount
	for key, count := range colorMap {
		r := uint8((key >> 16) & 0xFF)
		g := uint8((key >> 8) & 0xFF)
		b := uint8(key & 0xFF)
		colors = append(colors, colorCount{
			color: color.RGBA{r, g, b, 255},
			count: count,
		})
	}
	
	for i := 0; i < len(colors)-1; i++ {
		for j := i + 1; j < len(colors); j++ {
			if colors[i].count < colors[j].count {
				colors[i], colors[j] = colors[j], colors[i]
			}
		}
	}
	
	result := []color.Color{}
	for i := 0; i < len(colors) && i < 3; i++ {
		c := colors[i].color.(color.RGBA)
		cf := colorful.Color{
			R: float64(c.R) / 255.0,
			G: float64(c.G) / 255.0,
			B: float64(c.B) / 255.0,
		}
		
		l, a, b := cf.Lab()
		l = math.Max(0.2, math.Min(0.4, l))
		adjusted := colorful.Lab(l, a, b).Clamped()
		
		result = append(result, color.RGBA{
			R: uint8(adjusted.R * 255),
			G: uint8(adjusted.G * 255),
			B: uint8(adjusted.B * 255),
			A: 255,
		})
	}
	
	if len(result) == 0 {
		result = append(result, color.RGBA{40, 40, 40, 255})
	}
	
	for len(result) < 2 {
		lastColor := result[len(result)-1].(color.RGBA)
		cf := colorful.Color{
			R: float64(lastColor.R) / 255.0,
			G: float64(lastColor.G) / 255.0,
			B: float64(lastColor.B) / 255.0,
		}
		h, s, v := cf.Hsv()
		h = math.Mod(h+30, 360)
		adjusted := colorful.Hsv(h, s*0.8, v*0.9)
		
		result = append(result, color.RGBA{
			R: uint8(adjusted.R * 255),
			G: uint8(adjusted.G * 255),
			B: uint8(adjusted.B * 255),
			A: 255,
		})
	}
	
	return result
}

func createGradientBackground(dc *gg.Context, colors []color.Color) {
	width := float64(dc.Width())
	height := float64(dc.Height())
	
	if len(colors) < 2 {
		colors = []color.Color{
			color.RGBA{40, 40, 40, 255},
			color.RGBA{20, 20, 20, 255},
		}
	}
	
	gradient := gg.NewLinearGradient(0, 0, width, height)
	
	gradient.AddColorStop(0, colors[0])
	gradient.AddColorStop(0.5, colors[1])
	if len(colors) > 2 {
		gradient.AddColorStop(1, colors[2])
	} else {
		gradient.AddColorStop(1, colors[0])
	}
	
	dc.SetFillStyle(gradient)
	dc.DrawRectangle(0, 0, width, height)
	dc.Fill()
	
	blur := imaging.Blur(dc.Image(), 30)
	dc.DrawImage(blur, 0, 0)
	
	overlay := color.RGBA{0, 0, 0, 100}
	dc.SetColor(overlay)
	dc.DrawRectangle(0, 0, width, height)
	dc.Fill()
}

func generateSpotifyPreview(albumArt []byte, albumName, artistName, year string) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(albumArt))
	if err != nil {
		return nil, err
	}
	
	resized := imaging.Resize(img, albumArtSize, albumArtSize, imaging.Lanczos)
	
	colors := extractDominantColors(resized)
	
	dc := gg.NewContext(previewWidth, previewHeight)
	
	createGradientBackground(dc, colors)
	
	albumX := float64(previewWidth-albumArtSize) / 2
	albumY := float64(marginTop)
	
	dc.Push()
	dc.DrawRoundedRectangle(albumX-5, albumY-5, albumArtSize+10, albumArtSize+10, 10)
	dc.SetColor(color.RGBA{0, 0, 0, 50})
	dc.Fill()
	dc.Pop()
	
	rounded := imaging.New(albumArtSize, albumArtSize, color.Transparent)
	mask := imaging.New(albumArtSize, albumArtSize, color.Transparent)
	maskDc := gg.NewContextForImage(mask)
	maskDc.DrawRoundedRectangle(0, 0, albumArtSize, albumArtSize, 8)
	maskDc.SetColor(color.White)
	maskDc.Fill()
	
	draw.DrawMask(rounded, rounded.Bounds(), resized, image.Point{}, mask, image.Point{}, draw.Over)
	
	dc.DrawImage(rounded, int(albumX), int(albumY))
	
	textY := albumY + albumArtSize + 60
	
	dc.SetColor(color.White)
	
	// Load the embedded Fira Sans font
	font, err := truetype.Parse(firaFontData)
	if err != nil {
		// Fallback to basic font if parsing fails
		dc.SetRGB(1, 1, 1)
		dc.DrawStringAnchored(artistName, float64(previewWidth)/2, textY, 0.5, 0.5)
		textY += 40
		
		dc.DrawStringAnchored(albumName, float64(previewWidth)/2, textY+10, 0.5, 0.5)
		textY += 60
		
		if year != "" {
			dc.SetRGB(0.8, 0.8, 0.8)
			dc.DrawStringAnchored(year, float64(previewWidth)/2, textY+10, 0.5, 0.5)
		}
	} else {
		// Artist name
		face := truetype.NewFace(font, &truetype.Options{Size: 32})
		dc.SetFontFace(face)
		lines := wrapText(dc, artistName, float64(previewWidth-100))
		for _, line := range lines {
			dc.DrawStringAnchored(line, float64(previewWidth)/2, textY, 0.5, 0.5)
			textY += 40
		}
		
		textY += 10
		
		// Album name (larger font)
		face = truetype.NewFace(font, &truetype.Options{Size: 42})
		dc.SetFontFace(face)
		dc.SetColor(color.White)
		lines = wrapText(dc, albumName, float64(previewWidth-100))
		for _, line := range lines {
			dc.DrawStringAnchored(line, float64(previewWidth)/2, textY, 0.5, 0.5)
			textY += 50
		}
		
		textY += 20
		
		// Year (smaller font)
		if year != "" {
			face = truetype.NewFace(font, &truetype.Options{Size: 24})
			dc.SetFontFace(face)
			dc.SetColor(color.RGBA{200, 200, 200, 255})
			dc.DrawStringAnchored(year, float64(previewWidth)/2, textY, 0.5, 0.5)
		}
	}
	
	var buf bytes.Buffer
	err = jpeg.Encode(&buf, dc.Image(), &jpeg.Options{Quality: 90})
	if err != nil {
		return nil, err
	}
	
	return buf.Bytes(), nil
}

func wrapText(dc *gg.Context, text string, maxWidth float64) []string {
	words := []string{text}
	w, _ := dc.MeasureString(text)
	
	if w <= maxWidth {
		return words
	}
	
	runes := []rune(text)
	maxChars := int(float64(len(runes)) * (maxWidth / w))
	
	if maxChars <= 0 {
		maxChars = 1
	}
	
	var lines []string
	for i := 0; i < len(runes); i += maxChars {
		end := i + maxChars
		if end > len(runes) {
			end = len(runes)
		}
		lines = append(lines, string(runes[i:end]))
	}
	
	return lines
}