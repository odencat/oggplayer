// Copyright 2016 Hajime Hoshi
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

//go:build example || jsgo
// +build example jsgo

// This is an example to implement an audio player.
// See examples/wav for a simpler example to play a sound file.

package main

import (
	"bytes"
	"fmt"
	"image/color"
	"io/ioutil"
	"log"
	"os"
	"time"

	"github.com/hajimehoshi/ebiten"
	"github.com/hajimehoshi/ebiten/audio"
	"github.com/hajimehoshi/ebiten/audio/vorbis"
	"github.com/hajimehoshi/ebiten/audio/wav"
	"github.com/hajimehoshi/ebiten/ebitenutil"
	raudio "github.com/hajimehoshi/ebiten/examples/resources/audio"
	"github.com/hajimehoshi/ebiten/inpututil"
	"github.com/hajimehoshi/oggloop"
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

type musicType int

const (
	typeOgg musicType = iota
)

func (t musicType) String() string {
	switch t {
	case typeOgg:
		return "Ogg"
	default:
		panic("not reached")
	}
}

// Player represents the current audio state.
type Player struct {
	audioContext *audio.Context
	audioPlayer  *audio.Player
	current      time.Duration
	total        time.Duration
	seBytes      []byte
	seCh         chan []byte
	volume128    int
	musicType    musicType
	introSample  int64
	loopSample   int64
}

func playerBarRect() (x, y, w, h int) {
	w, h = 300, 4
	x = (screenWidth - w) / 2
	y = screenHeight - h - 16
	return
}

func NewPlayer(audioContext *audio.Context, musicType musicType) (*Player, error) {
	type audioStream interface {
		audio.ReadSeekCloser
		Length() int64
	}

	const bytesPerSample = 4 // TODO: This should be defined in audio package

	var s audioStream
	var introSample, loopSample int64
	switch musicType {
	case typeOgg:
		var err error
		var dat []byte
		oggPath := "test/battle01.ogg"
		dat, err = os.ReadFile(oggPath)
		if err != nil {
			panic(err)
		}

		introSample, loopSample, err = oggloop.Read(bytes.NewReader(dat))
		if err != nil {
			// Ignore oggloop's error.
			log.Printf("oggloop error: %s, %v", oggPath, err)
		}
		s, err = vorbis.Decode(audioContext, audio.BytesReadSeekCloser(dat))
		if err != nil {
			return nil, err
		}
	default:
		panic("not reached")
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
		musicType:    musicType,
		introSample:  introSample,
		loopSample:   loopSample,
	}
	if player.total == 0 {
		player.total = 1
	}
	player.audioPlayer.Play()
	go func() {
		s, err := wav.Decode(audioContext, audio.BytesReadSeekCloser(raudio.Jab_wav))
		if err != nil {
			log.Fatal(err)
			return
		}
		b, err := ioutil.ReadAll(s)
		if err != nil {
			log.Fatal(err)
			return
		}
		player.seCh <- b
	}()
	return player, nil
}

func (p *Player) Close() error {
	return p.audioPlayer.Close()
}

func (p *Player) loopStartInSecond() float64 {
	return float64(p.introSample) / (sampleRate)
}

func (p *Player) loopLengthInSecond() float64 {
	return float64(p.loopSample) / (sampleRate)
}

func (p *Player) loopEndInSecond() float64 {
	return float64(p.introSample+p.loopSample) / (sampleRate)
}

func (p *Player) update() error {
	select {
	case p.seBytes = <-p.seCh:
		close(p.seCh)
		p.seCh = nil
	default:
	}

	if p.audioPlayer.IsPlaying() {
		pos := p.audioPlayer.Current()
		if pos > time.Duration(p.loopStartInSecond())*time.Second {
			pos = (pos-time.Duration(p.loopStartInSecond())*time.Second)%(time.Duration(p.loopLengthInSecond())*time.Second) + time.Duration(p.loopStartInSecond())*time.Second
		}
		p.current = pos
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
	if !inpututil.IsKeyJustPressed(ebiten.KeyS) {
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
	cx = int(time.Duration(w)*(time.Duration(p.loopStartInSecond())*time.Second)/p.total) + x - cw/2
	ebitenutil.DrawRect(screen, float64(cx), float64(cy), float64(cw), float64(ch), loopCursorColor)

	// Draw the loop end on the bar.
	cx = int(time.Duration(w)*(time.Duration(p.loopEndInSecond())*time.Second)/p.total) + x - cw/2
	ebitenutil.DrawRect(screen, float64(cx), float64(cy), float64(cw), float64(ch), loopCursorColor)

	loopStartStr := fmt.Sprintf("%02d:%02d", int(p.loopStartInSecond())/60, int(p.loopStartInSecond())%60)
	loopEndStr := fmt.Sprintf("%02d:%02d", int(p.loopEndInSecond())/60, int(p.loopEndInSecond())%60)
	// Draw the debug message.
	msg := fmt.Sprintf(`Press S to toggle Play/Pause
Press Z or X to change volume of the music
Current Volume: %d/128
Current Time: %s (%d),
Loop Start: %s (%d)
Loop End: %s (%d)
`, int(p.audioPlayer.Volume()*128), currentTimeStr, (c*sampleRate)/time.Second, loopStartStr, p.introSample, loopEndStr, p.introSample+p.loopSample)
	ebitenutil.DebugPrint(screen, msg)
}

type Game struct {
	musicPlayer   *Player
	musicPlayerCh chan *Player
	errCh         chan error
}

func NewGame() (*Game, error) {
	audioContext, err := audio.NewContext(sampleRate)
	if err != nil {
		return nil, err
	}

	m, err := NewPlayer(audioContext, typeOgg)
	if err != nil {
		return nil, err
	}

	ebiten.SetRunnableOnUnfocused(true)

	return &Game{
		musicPlayer:   m,
		musicPlayerCh: make(chan *Player),
		errCh:         make(chan error),
	}, nil
}

func (g *Game) Update(screen *ebiten.Image) error {
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
	return nil
}

func (g *Game) Draw(screen *ebiten.Image) {
	if g.musicPlayer != nil {
		g.musicPlayer.draw(screen)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return screenWidth, screenHeight
}

func main() {
	ebiten.SetWindowSize(screenWidth*2, screenHeight*2)
	ebiten.SetWindowTitle("Audio (Ebiten Demo)")
	g, err := NewGame()
	if err != nil {
		log.Fatal(err)
	}
	if err := ebiten.RunGame(g); err != nil {
		log.Fatal(err)
	}
}
