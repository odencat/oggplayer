// Copyright 2021 Odencat
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"fmt"
	"image/color"
	"log"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/audio/vorbis"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/oggloop"
	"github.com/sqweek/dialog"
)

const (
	screenWidth  = 320
	screenHeight = 240

	sampleRate = 48000
)

var (
	playerBarColor     = color.RGBA{0x80, 0x80, 0x80, 0xff}
	playerCurrentColor = color.RGBA{0xff, 0xff, 0xff, 0xff}
	loopCursorColor    = color.RGBA{0xff, 0xff, 0x80, 0xff}
)

// Player represents the current audio state.
type Player struct {
	audioContext *audio.Context
	audioPlayer  *audio.Player
	current      time.Duration
	total        time.Duration
	seBytes      []byte
	seCh         chan []byte
	volume128    int
	introSample  int64
	loopSample   int64
}

func playerBarRect() (x, y, w, h int) {
	w, h = 300, 4
	x = (screenWidth - w) / 2
	y = screenHeight - h - 16
	return
}

func NewPlayer(audioContext *audio.Context, oggPath string) (*Player, error) {
	const bytesPerSample = 4 // TODO: This should be defined in audio package

	var s *vorbis.Stream
	var introSample, loopSample int64

	var err error
	var dat []byte
	dat, err = os.ReadFile(oggPath)
	if err != nil {
		return nil, err
	}

	introSample, loopSample, err = oggloop.Read(bytes.NewReader(dat))
	if err != nil {
		// Ignore oggloop's error.
		log.Printf("oggloop error: %s, %v", oggPath, err)
	}
	s, err = vorbis.Decode(audioContext, bytes.NewReader(dat))
	if err != nil {
		return nil, err
	}

	s2 := audio.NewInfiniteLoopWithIntro(s, introSample*bytesPerSample, loopSample*bytesPerSample)

	p, err := audio.NewPlayer(audioContext, s2)
	if err != nil {
		return nil, err
	}
	player := &Player{
		audioContext: audioContext,
		audioPlayer:  p,
		total:        time.Second * time.Duration(s.Length()) / bytesPerSample / sampleRate,
		volume128:    128,
		seCh:         make(chan []byte),
		introSample:  introSample,
		loopSample:   loopSample,
	}
	if player.total == 0 {
		player.total = 1
	}
	player.audioPlayer.Play()
	return player, nil
}

func (p *Player) Resume() {
	if !p.audioPlayer.IsPlaying() {
		p.audioPlayer.Play()
	}
}

func (p *Player) Pause() {
	if p.audioPlayer.IsPlaying() {
		p.audioPlayer.Pause()
	}
}

func (p *Player) Close() error {
	return p.audioPlayer.Close()
}

func (p *Player) loopStartInSecond() float64 {
	return float64(p.introSample) / sampleRate
}

func (p *Player) loopLengthInSecond() float64 {
	return float64(p.loopSample) / sampleRate
}

func (p *Player) loopEndInSecond() float64 {
	return float64(p.introSample+p.loopSample) / sampleRate
}

func (p *Player) update() error {
	select {
	case p.seBytes = <-p.seCh:
		close(p.seCh)
		p.seCh = nil
	default:
	}

	if p.audioPlayer.IsPlaying() {
		curentSample := int64(p.audioPlayer.Current() * sampleRate / time.Second)
		newSample := curentSample
		if curentSample > p.introSample {
			newSample = (curentSample-p.introSample)%p.loopSample + p.introSample
		}
		p.current = (time.Duration(newSample) * time.Second) / time.Duration(sampleRate)
	}
	p.seekBarIfNeeded()
	p.switchPlayStateIfNeeded()
	p.updateVolumeIfNeeded()

	return nil
}

func (p *Player) updateVolumeIfNeeded() {
	if ebiten.IsKeyPressed(ebiten.KeyZ) {
		p.volume128--
	}
	if ebiten.IsKeyPressed(ebiten.KeyX) {
		p.volume128++
	}
	if p.volume128 < 0 {
		p.volume128 = 0
	}
	if 128 < p.volume128 {
		p.volume128 = 128
	}
	p.audioPlayer.SetVolume(float64(p.volume128) / 128)
}

func (p *Player) switchPlayStateIfNeeded() {
	if !inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		return
	}
	if p.audioPlayer.IsPlaying() {
		p.audioPlayer.Pause()
		return
	}
	p.audioPlayer.Play()
}

func (p *Player) seekBarIfNeeded() {
	if !inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		return
	}

	// Calculate the next seeking position from the current cursor position.
	x, y := ebiten.CursorPosition()
	bx, by, bw, bh := playerBarRect()
	const padding = 4
	if y < by-padding || by+bh+padding <= y {
		return
	}
	if x < bx || bx+bw <= x {
		return
	}
	pos := time.Duration(x-bx) * p.total / time.Duration(bw)
	p.current = pos
	p.audioPlayer.Seek(pos)
}

func (p *Player) draw(screen *ebiten.Image) {
	// Draw the bar.
	x, y, w, h := playerBarRect()
	ebitenutil.DrawRect(screen, float64(x), float64(y), float64(w), float64(h), playerBarColor)

	// Draw the cursor on the bar.
	c := p.current
	cw, ch := 4, 10
	cx := int(time.Duration(w)*c/p.total) + x - cw/2
	cy := y - (ch-h)/2
	ebitenutil.DrawRect(screen, float64(cx), float64(cy), float64(cw), float64(ch), playerCurrentColor)

	// Compose the curren time text.
	m := (c / time.Minute) % 100
	s := (c / time.Second) % 60
	currentTimeStr := fmt.Sprintf("%02d:%02d", m, s)

	// Draw the loop start on the bar.
	cx = int((time.Duration(w*int(p.introSample)/sampleRate)*time.Second)/p.total) + x - cw/2
	ebitenutil.DrawRect(screen, float64(cx), float64(cy), float64(cw), float64(ch), loopCursorColor)

	// Draw the loop end on the bar.
	cx = int((time.Duration(w*int(p.introSample+p.loopSample)/sampleRate)*time.Second)/p.total) + x - cw/2
	ebitenutil.DrawRect(screen, float64(cx), float64(cy), float64(cw), float64(ch), loopCursorColor)

	loopStartStr := fmt.Sprintf("%02d:%02d", int(p.loopStartInSecond())/60, int(p.loopStartInSecond())%60)
	loopEndStr := fmt.Sprintf("%02d:%02d", int(p.loopEndInSecond())/60, int(p.loopEndInSecond())%60)
	// Draw the debug message.
	msg := fmt.Sprintf(`Press Space to toggle Play/Pause
Press Z or X to change volume of the music
Current Volume: %d/128
Loop Start: %s (%d)
Loop End: %s (%d)
Current Time: %s (%d)
`, int(p.audioPlayer.Volume()*128), loopStartStr, p.introSample, loopEndStr, p.introSample+p.loopSample, currentTimeStr, (c*sampleRate)/time.Second)
	ebitenutil.DebugPrint(screen, msg)
}

type Game struct {
	audioContext  *audio.Context
	musicPlayer   *Player
	musicPlayerCh chan *Player
	fileCh        chan string
	errCh         chan error
}

func NewGame() (*Game, error) {
	audioContext := audio.NewContext(sampleRate)

	ebiten.SetRunnableOnUnfocused(true)

	return &Game{
		audioContext:  audioContext,
		musicPlayer:   nil,
		musicPlayerCh: make(chan *Player),
		errCh:         make(chan error),
	}, nil
}

func (g *Game) Update() error {
	select {
	case p := <-g.musicPlayerCh:
		g.musicPlayer = p
	case err := <-g.errCh:
		return err
	default:
	}

	if g.musicPlayer != nil {
		if err := g.musicPlayer.update(); err != nil {
			return err
		}
	}

	if err := g.openFileIfNeeded(); err != nil {
		return err
	}

	return nil
}

func (g *Game) openFile() {
	filename, err := dialog.File().Filter("Ogg file", "ogg").Load()
	if err != dialog.Cancelled {
		g.fileCh <- filename
	}
	g.fileCh <- ""
}

func (g *Game) openFileIfNeeded() error {
	select {
	case filename := <-g.fileCh:
		if filename != "" {
			fmt.Println("open ogg file", filename)
			if g.musicPlayer != nil {
				g.musicPlayer.Close()
			}

			m, err := NewPlayer(g.audioContext, filename)
			if err != nil {
				return err
			}

			g.musicPlayer = m
		}
	default:
	}

	if !inpututil.IsKeyJustPressed(ebiten.KeyF) {
		return nil
	}
	if g.musicPlayer != nil {
		g.musicPlayer.Pause()
	}

	g.fileCh = make(chan string)
	go g.openFile()

	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	if g.musicPlayer == nil {
		ebitenutil.DebugPrint(screen, `Press F to load an ogg file`)
		return
	}
	g.musicPlayer.draw(screen)
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

func main() {
	ebiten.SetWindowSize(screenWidth*2, screenHeight*2)
	ebiten.SetWindowTitle("Ogg Loop Checker")
	g, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
