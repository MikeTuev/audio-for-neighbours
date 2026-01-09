#!/bin/sh

cat >/etc/systemd/system/audio-for-neighbours.service <<'EOF'
[Unit]
Description=Audio for Neighbours
After=network-online.target bluetooth.service dbus.service bluealsa.service
Wants=network-online.target bluetooth.service dbus.service bluealsa.service

[Service]
Type=simple
User=root
WorkingDirectory=/home/mike/aud

ExecStartPre=/opt/audio-for-neighbours/bt-connect-speaker.sh

ExecStart=/opt/audio-for-neighbours

StandardOutput=journal
StandardError=journal

Restart=always
RestartSec=2

KillSignal=SIGINT
TimeoutStopSec=10

[Install]
WantedBy=multi-user.target
EOF


systemctl daemon-reload
systemctl enable --now audio-for-neighbours.service

#log:
journalctl -u audio-for-neighbours -n 200 --no-pager
