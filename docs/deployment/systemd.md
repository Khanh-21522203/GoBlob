# systemd Deployment

```bash
sudo cp systemd/*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now goblob-master goblob-volume goblob-filer goblob-s3
```
