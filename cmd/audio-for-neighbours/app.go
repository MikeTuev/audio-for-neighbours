package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type app struct {
	player   *audioPlayer
	notifier *telegramNotifier
	snapshot *snapshotter
	presence *presenceTracker

	mu               sync.Mutex
	paused           bool
	pausedBySchedule bool
	pausedByMotion   bool
	pausedByManual   bool
	pausedByPresence bool
	forcePlay        bool
	lastMotion       bool
	motionTimer      *time.Timer
	currentFile      string
	onlineTargets    []string
	motionSnapCancel context.CancelFunc
}

func newApp(player *audioPlayer, notifier *telegramNotifier) *app {
	return &app{
		player:   player,
		notifier: notifier,
		presence: newPresenceTracker(appConfig.PresenceClearDelay),
	}
}

func (a *app) setSnapshotter(snapshot *snapshotter) {
	a.snapshot = snapshot
}

func (a *app) runFileNotifications(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case file := <-a.player.fileStarted():
			a.mu.Lock()
			a.currentFile = file
			a.mu.Unlock()
			a.notify(fmt.Sprintf("Now playing: %s", file))
		}
	}
}

func (a *app) runScheduleLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	a.setSchedulePause(isQuietHours(time.Now()), "schedule check")
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.setSchedulePause(isQuietHours(time.Now()), "schedule check")
		}
	}
}

func (a *app) runPresenceEvents(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-a.presence.Events():
			a.handlePresenceEvent(evt)
		}
	}
}

func (a *app) handlePresenceUpdate(online []string) {
	events := a.presence.Update(online)
	for _, evt := range events {
		a.handlePresenceEvent(evt)
	}

	a.applyPresenceState()
}

func (a *app) handlePresenceEvent(evt presenceEvent) {
	if evt.Online {
		a.notify(fmt.Sprintf("Presence online: %s", evt.Name))
	} else {
		a.notify(fmt.Sprintf("Presence offline: %s", evt.Name))
	}
	a.applyPresenceState()
}

func (a *app) applyPresenceState() {
	online := a.presence.CurrentOnline()
	a.mu.Lock()
	changed := !stringSlicesEqual(a.onlineTargets, online)
	a.onlineTargets = online
	a.mu.Unlock()
	if !changed {
		return
	}

	if len(online) > 0 {
		a.setPresencePause(true, fmt.Sprintf("presence detected (%s)", strings.Join(online, ", ")))
		return
	}
	a.setPresencePause(false, "presence cleared (debounced)")
}
func (a *app) handleMotionUpdate(detected bool, names []string) {
	a.mu.Lock()
	a.lastMotion = detected
	a.mu.Unlock()

	if detected {
		a.setMotionPause(true, fmt.Sprintf("motion detected (%s)", strings.Join(names, ", ")))
		a.startMotionSnapshots()
		return
	}

	a.stopMotionSnapshots()
	a.startMotionResumeTimer()
}

func (a *app) startMotionResumeTimer() {
	a.mu.Lock()
	if a.motionTimer != nil {
		a.motionTimer.Stop()
	}
	a.motionTimer = time.AfterFunc(appConfig.MotionResumeDelay, func() {
		log.Printf("motion resume timer fired")
		a.setMotionPause(false, "motion cleared 5m")
	})
	log.Printf("motion resume timer scheduled for %s", appConfig.MotionResumeDelay)
	a.mu.Unlock()
}

func (a *app) setSchedulePause(paused bool, trigger string) {
	a.mu.Lock()
	a.pausedBySchedule = paused
	a.mu.Unlock()
	a.applyState(trigger)
}

func (a *app) setMotionPause(paused bool, trigger string) {
	a.mu.Lock()
	a.pausedByMotion = paused
	if paused && a.motionTimer != nil {
		if a.motionTimer.Stop() {
			log.Printf("motion resume timer canceled (motion active)")
		}
		a.motionTimer = nil
	}
	a.mu.Unlock()
	a.applyState(trigger)
}

func (a *app) setPresencePause(paused bool, trigger string) {
	a.mu.Lock()
	a.pausedByPresence = paused
	a.mu.Unlock()
	a.applyState(trigger)
}

func (a *app) setManualPause(paused bool, trigger string) {
	a.mu.Lock()
	a.pausedByManual = paused
	a.mu.Unlock()
	a.applyState(trigger)
}

func (a *app) setForcePlay(enabled bool, trigger string) {
	a.mu.Lock()
	a.forcePlay = enabled
	a.mu.Unlock()
	a.applyState(trigger)
}

func (a *app) applyState(trigger string) {
	a.mu.Lock()
	shouldPause := a.pausedBySchedule || a.pausedByMotion || a.pausedByPresence
	if a.forcePlay {
		shouldPause = false
	}
	if a.pausedByManual {
		shouldPause = true
	}
	wasPaused := a.paused
	if shouldPause == wasPaused {
		a.mu.Unlock()
		return
	}
	a.paused = shouldPause
	reasons := a.pauseReasonsLocked()
	currentFile := a.currentFile
	a.mu.Unlock()

	a.player.setPaused(shouldPause)

	if shouldPause {
		a.notify(fmt.Sprintf("Playback paused (%s). Reasons: %s. Current: %s", trigger, strings.Join(reasons, ", "), currentFile))
		return
	}
	a.notify(fmt.Sprintf("Playback resumed (%s). Current: %s", trigger, currentFile))
}

func (a *app) pauseReasonsLocked() []string {
	var reasons []string
	if a.pausedBySchedule {
		reasons = append(reasons, "quiet hours")
	}
	if a.pausedByMotion {
		reasons = append(reasons, "motion")
	}
	if a.pausedByPresence {
		if len(a.onlineTargets) > 0 {
			reasons = append(reasons, "presence:"+strings.Join(a.onlineTargets, ", "))
		} else {
			reasons = append(reasons, "presence")
		}
	}
	if a.forcePlay {
		reasons = append(reasons, "forced play")
	}
	if a.pausedByManual {
		reasons = append(reasons, "manual")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "none")
	}
	return reasons
}

func (a *app) handleCommand(cmd string) string {
	switch cmd {
	case "pause", "stop", "disable":
		a.setForcePlay(false, "telegram")
		a.setManualPause(true, "telegram")
		return "Playback paused by manual command."
	case "play", "start", "enable":
		a.setManualPause(false, "telegram")
		a.setForcePlay(true, "telegram")
		return "Playback forced on by manual command."
	case "auto":
		a.setForcePlay(false, "telegram")
		a.setManualPause(false, "telegram")
		return "Playback returned to automatic control."
	case "status":
		a.mu.Lock()
		paused := a.paused
		reasons := a.pauseReasonsLocked()
		current := a.currentFile
		forced := a.forcePlay
		online := strings.Join(a.onlineTargets, ", ")
		a.mu.Unlock()
		return fmt.Sprintf("Paused=%v, forced=%v, reasons=%s, current=%s, online=%s", paused, forced, strings.Join(reasons, ", "), current, online)
	case "snapshot":
		if a.snapshot == nil || a.notifier == nil {
			return "Snapshot not available."
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		image, err := a.snapshot.getSnapshot(ctx)
		if err != nil {
			return fmt.Sprintf("Snapshot error: %v", err)
		}
		a.notifier.sendPhotoBytes("snapshot.jpg", image)
		return "Snapshot sent."
	default:
		return "Commands: /play (force on), /pause, /auto, /status, /snapshot, /enable, /disable"
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (a *app) notify(msg string) {
	log.Printf("%s", msg)
	if a.notifier == nil {
		return
	}
	a.notifier.send(msg)
}

func (a *app) startMotionSnapshots() {
	if a.snapshot == nil || a.notifier == nil {
		return
	}

	a.mu.Lock()
	if a.motionSnapCancel != nil {
		a.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.motionSnapCancel = cancel
	a.mu.Unlock()

	go a.motionSnapshotLoop(ctx)
}

func (a *app) stopMotionSnapshots() {
	a.mu.Lock()
	cancel := a.motionSnapCancel
	a.motionSnapCancel = nil
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *app) motionSnapshotLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			image, err := a.snapshot.getSnapshot(ctx)
			if err != nil {
				log.Printf("snapshot error: %v", err)
				continue
			}
			a.notifier.sendPhotoBytes("motion.jpg", image)
		}
	}
}
