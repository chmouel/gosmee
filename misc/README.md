# System services

## macOS:

```shell
cp com.chmouel.gosmee.plist ~/Library/LaunchAgents/
launchctl load -w ~/Library/LaunchAgents/com.chmouel.gosmee.plist
```

## Linux

```shell
mkdir -p $HOME/.config/systemd/user
cp gosmee.service $HOME/.config/systemd/user/
systemctl --user enable --now gosmee
```
