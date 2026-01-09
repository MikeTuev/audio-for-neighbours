package main

import (
	"context"
	"log"
	"net/http"
	"time"
)

func main() {
	ctx := context.Background()

	cfg, err := loadConfig("config.yaml")
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	appConfig = cfg

	player := newAudioPlayer(appConfig.AudioDir)
	notifier, err := newTelegramNotifier(appConfig.Telegram.Token, appConfig.Telegram.ChatID)
	if err != nil {
		log.Printf("telegram init error: %v", err)
	}

	app := newApp(player, notifier)

	go player.run(ctx)
	go app.runFileNotifications(ctx)
	go app.runScheduleLoop(ctx)
	go app.runPresenceEvents(ctx)

	if notifier != nil {
		go notifier.run(ctx, app.handleCommand)
	}

	client := &http.Client{
		Transport: &digestTransport{
			username: appConfig.Camera.Username,
			password: appConfig.Camera.Password,
			rt:       http.DefaultTransport,
		},
		Timeout: 30 * time.Second,
	}

	dev, err := newOnvifDevice(client)
	if err != nil {
		log.Fatalf("connect device: %v", err)
	}
	app.setSnapshotter(newSnapshotter(client, dev))

	go pollMotion(ctx, client, dev, app.handleMotionUpdate)

	routerClient := newRouterClient(appConfig.Router.BaseURL, appConfig.Router.Username, appConfig.Router.Password, appConfig.Router.Lang)
	go pollPresence(ctx, routerClient, appConfig.PresenceTargets, app.handlePresenceUpdate)

	select {}
}
