package main

import (
	"image"
	"os"

	"fyne.io/fyne/v2/app"
	_ "github.com/malashin/dds"

	// TGA must be the last registered image format due to not having magic prefix.
	// Every image file will be treated as TGA if registered magic is not found.
	_ "github.com/ftrvxmtrx/tga"
)

const appVersion = "0.6.0"

var (
	binPath string
	cfg     Config
)

// modPaths is the search order used for every lookup: the game folder first,
// then each dependency mod, then the mod the selected focus tree lives in.
// Later entries win when the same asset is declared twice.
var modPaths []string

var (
	focusMap    = make(map[string]Focus)
	gfxMap      = make(map[string]SpriteType)
	fontMap     = make(map[string]BitmapFont)
	locMap      = make(map[string]string)
	scriptedLoc = make(map[string]string)
	gui         FocusGUI
)

const (
	spacingX = 131
	spacingY = 63
)

type Dir int

const (
	U Dir = 1
	D Dir = 2
	L Dir = 4
	R Dir = 8
	S Dir = 16
)

var (
	UDdash   image.Image
	ULdash   image.Image
	URdash   image.Image
	DLdash   image.Image
	DRdash   image.Image
	LRdash   image.Image
	UDLdash  image.Image
	UDRdash  image.Image
	ULRdash  image.Image
	DLRdash  image.Image
	UDLRdash image.Image
	UD       image.Image
	UL       image.Image
	UR       image.Image
	DL       image.Image
	DR       image.Image
	LR       image.Image
	UDL      image.Image
	UDR      image.Image
	ULR      image.Image
	DLR      image.Image
	UDLR     image.Image
)

var utf8bom = []byte{0xEF, 0xBB, 0xBF}

type Focus struct {
	ID                 string
	Icon               string
	Text               string
	X                  int
	Y                  int
	RelativePositionID string
	Prerequisite       [][]string
	MutuallyExclusive  []string
	AllowBranch        bool
	Available          bool
	Shared             bool
	Source             string
	Children           []Child
	In                 map[int]FocusLine
	Out                FocusLine
}

type Child struct {
	ID    string
	Solid bool
}

type FocusLine struct {
	Dir Dir
}

type SpriteType struct {
	Name        string
	TextureFile string
	NoOfFrames  int
	Image       image.Image
}

type BitmapFont struct {
	Name      string
	Path      string
	Fontfiles []string
}

type FocusGUI struct {
	NationalFocusTitle         InstantTextboxType
	NationalFocusItem          ContainerWindowType
	BG                         ButtonType
	Symbol                     ButtonType
	Name                       InstantTextboxType
	NationalFocusLink          ContainerWindowType
	Link                       IconType
	NationalFocusExclusiveItem ContainerWindowType
	Link1                      IconType
	Link2                      IconType
	Left                       IconType
	Right                      IconType
	Mid                        IconType
	FocusSpacing               image.Point
	LinkSpacing                image.Point
	LinkOffsets                image.Point
	LinkBegin                  image.Point
	LinkEnd                    image.Point
	ExclusiveOffset            image.Point
	ExclusiveOffsetLeft        image.Point
	ExclusivePositioning       image.Point
}

type InstantTextboxType struct {
	Name              string
	Position          image.Point
	Orientation       string
	Text              string
	Font              string
	MaxWidth          int
	MaxHeight         int
	Format            string
	VerticalAlignment string
}

type ContainerWindowType struct {
	Name     string
	Position image.Point
	Width    int
	Height   int
}

type ButtonType struct {
	Name           string
	Position       image.Point
	SpriteType     string
	CenterPosition string
	Orientation    string
}

type IconType struct {
	Name       string
	Position   image.Point
	SpriteType string
	Frame      int
}

func main() {
	bin, err := os.Executable()
	if err != nil {
		// Falling back to the working directory is better than refusing to start.
		binPath, _ = os.Getwd()
	} else {
		binPath = resolveBinPath(bin)
	}

	cfg = loadConfig()

	a := app.New()
	setupUI(a)
}
