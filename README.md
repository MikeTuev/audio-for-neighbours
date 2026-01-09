audio-for-neighbours
====================

Audio player to automatically run a vibration speaker for neighbors.

Features
--------
- Huawei HG8245 router presence check: track specific device names; if any are online, playback is paused (assumes you are home).
- ONVIF IP camera motion detection: pause playback on motion and send Telegram snapshots while motion is active.
- Quiet hours schedule and manual control via Telegram.

Configuration
-------------
Configuration is loaded from `config.yaml`. See `config_example.yaml` for a template.

Top-level keys:
- `audio_dir`: Path to the folder with audio files.
- `pull_timeout`: ONVIF PullPoint timeout, e.g. `PT10S`.
- `message_limit`: ONVIF PullPoint message limit per poll.
- `motion_resume_delay`: How long to wait after motion clears before resuming playback, e.g. `2m`.
- `presence_clear_delay`: Debounce time before treating devices as offline, e.g. `4m`.
- `use_ws_security`: Enable WS-Security for ONVIF requests if required by your camera.
- `presence_targets`: List of device names from the router UI to treat as "home".

Camera:
- `camera.ip`: Camera IP address.
- `camera.username`: ONVIF username.
- `camera.password`: ONVIF password.

Router:
- `router.base_url`: Router base URL, e.g. `http://10.0.0.1`.
- `router.username`: Router username.
- `router.password`: Router password.
- `router.lang`: Router UI language, e.g. `english`.

Telegram:
- `telegram.token`: Bot token.
- `telegram.chat_id`: Chat ID to send messages and receive commands.

Notes
-----
- Audio files are played in a loop (by filename), and new files dropped into the audio folder will be picked up when a file ends.
- Telegram commands: `/play`, `/pause`, `/auto`, `/status`, `/snapshot`.
