# System services

Somne system integrations, make sure to edit the downloaded file and adjust the target URL before applying it directly on your system/cluster

## macOS

```shell
mkdir -p $HOME/Library/LaunchAgents/
curl -L https://raw.githubusercontent.com/chmouel/gosmee/main/misc/com.chmouel.gosmee.plist -o $HOME/Library/LaunchAgents/com.chmouel.gosmee.plist
$EDITOR $HOME/Library/LaunchAgents/com.chmouel.gosmee.plist

launchctl load -w ~/Library/LaunchAgents/com.chmouel.gosmee.plist
```

## Linux / Systemd

```shell
mkdir -p $HOME/.config/systemd/user
curl -L https://raw.githubusercontent.com/chmouel/gosmee/main/misc/gosmee.service -o $HOME/.config/systemd/user/gosmee.service
$EDITOR  $HOME/.config/systemd/user/gosmee.service
systemctl --user enable --now gosmee
```

## Kubernetes

```shell
curl -LO https://raw.githubusercontent.com/chmouel/gosmee/main/misc/kubernetes-deployment.yaml
$EDITOR kubernetes-deployment.yaml
kubectl apply -f kubernetes-deployment.yaml
```
