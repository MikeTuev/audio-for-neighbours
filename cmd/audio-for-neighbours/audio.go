package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/faiface/beep"
	"github.com/faiface/beep/mp3"
	"github.com/faiface/beep/speaker"
	"github.com/faiface/beep/wav"
)

type audioPlayer struct {
	dir            string
	baseSampleRate beep.SampleRate
	ctrlMu         sync.Mutex
	ctrl           *beep.Ctrl
	pausedMu       sync.Mutex
	paused         bool
	fileStartedCh  chan string
}

func newAudioPlayer(dir string) *audioPlayer {
	return &audioPlayer{
		dir:           dir,
		fileStartedCh: make(chan string, 1),
	}
}

func (p *audioPlayer) fileStarted() <-chan string {
	return p.fileStartedCh
}

func (p *audioPlayer) setPaused(paused bool) {
	p.pausedMu.Lock()
	p.paused = paused
	p.pausedMu.Unlock()

	p.ctrlMu.Lock()
	ctrl := p.ctrl
	p.ctrlMu.Unlock()

	if ctrl == nil {
		return
	}
	speaker.Lock()
	ctrl.Paused = paused
	speaker.Unlock()
}

func (p *audioPlayer) isPaused() bool {
	p.pausedMu.Lock()
	defer p.pausedMu.Unlock()
	return p.paused
}

func (p *audioPlayer) run(ctx context.Context) {
	var lastPlayed string
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		files, err := listAudioFiles(p.dir)
		if err != nil {
			log.Printf("audio list error: %v", err)
			time.Sleep(30 * time.Second)
			continue
		}
		if len(files) == 0 {
			log.Printf("audio: no files in %s", p.dir)
			time.Sleep(30 * time.Second)
			continue
		}

		nextIndex := nextFileIndex(files, lastPlayed)
		file := files[nextIndex]
		if err := p.playFile(ctx, file); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("audio play error: %v", err)
		}
		lastPlayed = file
		if ctx.Err() != nil {
			return
		}
	}
}

func (p *audioPlayer) playFile(ctx context.Context, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	streamer, format, err := decodeAudio(f, path)
	if err != nil {
		return err
	}
	defer streamer.Close()

	if p.baseSampleRate == 0 {
		speaker.Init(format.SampleRate, format.SampleRate.N(time.Second/10))
		p.baseSampleRate = format.SampleRate
	}

	finalStreamer := beep.Streamer(streamer)
	if format.SampleRate != p.baseSampleRate {
		finalStreamer = beep.Resample(4, format.SampleRate, p.baseSampleRate, finalStreamer)
	}

	ctrl := &beep.Ctrl{Streamer: finalStreamer, Paused: p.isPaused()}
	p.ctrlMu.Lock()
	p.ctrl = ctrl
	p.ctrlMu.Unlock()

	select {
	case p.fileStartedCh <- filepath.Base(path):
	default:
	}

	done := make(chan struct{})
	speaker.Play(beep.Seq(ctrl, beep.Callback(func() {
		close(done)
	})))

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func decodeAudio(r io.ReadSeeker, path string) (beep.StreamSeekCloser, beep.Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return mp3.Decode(io.NopCloser(r))
	case ".wav":
		return wav.Decode(io.NopCloser(r))
	default:
		return nil, beep.Format{}, fmt.Errorf("unsupported audio format: %s", path)
	}
}

func listAudioFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".mp3" || ext == ".wav" {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func nextFileIndex(files []string, lastPlayed string) int {
	if len(files) == 0 {
		return 0
	}
	if lastPlayed == "" {
		return 0
	}
	for i, file := range files {
		if file == lastPlayed {
			return (i + 1) % len(files)
		}
	}
	return 0
}
