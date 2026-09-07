# External HTTPS and certificate monitoring

Run this host-native systemd monitor on an operator-controlled Linux machine outside the customer VPS and cluster. It checks public HTTPS with verified certificates every minute and sends email directly to an external SMTP provider. It must not depend on the customer cluster's ingress, DNS resolver, Alertmanager, or SMTP relay. A Raspberry Pi at another site can cover VPS failure; it does not cover failure of that Pi or its Internet connection.

The initial implementation is locally tested, including real TLS connections, untrusted certificates, HTTP failure, certificate age, notification retries, and recovery transitions. Live installation and actual email delivery remain separate qualification steps.

## Configuration

Create `/etc/deployer-external-monitor/config.json`, owned by root with mode 0600. Use public health URLs that return a successful status without authentication. Up to eight checks are supported per service instance. Redirects are treated as failures.

```json
{
  "environment": "customer-a",
  "failure_threshold": 3,
  "repeat_seconds": 14400,
  "checks": [
    {
      "name": "public-api",
      "url": "https://api.example.com/readyz",
      "expected_status": 200,
      "minimum_certificate_days": 14
    }
  ],
  "smtp": {
    "host": "smtp.example.com",
    "port": 465,
    "tls_mode": "ssl",
    "username": "monitor@example.com",
    "password_file": "/etc/deployer-external-monitor/smtp-password",
    "from": "monitor@example.com",
    "to": ["operator@example.com"]
  }
}
```

Keep the SMTP password in its own root-owned 0600 file. The directory must be root-owned 0700. `starttls` on port 587 is also supported and requires successful certificate verification before credentials are sent. Do not use a local relay hosted on the monitored VPS.

From a reviewed checkout on the monitoring machine:

```sh
sudo install -d -m 0700 /etc/deployer-external-monitor
sudo install -d -m 0755 /usr/local/libexec
sudo install -m 0755 scripts/external-uptime-monitor.py /usr/local/libexec/external-uptime-monitor.py
sudo install -m 0644 deploy/external-monitor/*.service deploy/external-monitor/*.timer /etc/systemd/system/
sudo python3 /usr/local/libexec/external-uptime-monitor.py --config /etc/deployer-external-monitor/config.json --check-only
sudo systemctl daemon-reload
sudo systemctl enable --now deployer-external-monitor.timer
```

Provision the protected configuration and password before running the check. `--check-only` makes HTTPS requests but never writes state or sends email. It returns nonzero when any check fails. Normal runs persist counters privately under `/var/lib/deployer-external-monitor`. After three consecutive failures, an alert is sent, repeated every four hours until recovery; recovery generates one message. Failed and partially rejected SMTP deliveries are retried. Logs omit URLs, credentials and SMTP error text.

Inspect `systemctl status deployer-external-monitor.timer` and `journalctl -u deployer-external-monitor.service`. A failed SMTP delivery makes the service fail visibly. That local failure cannot notify you if this monitoring host or its email path is unavailable; monitor the host separately if that failure mode must be covered.

## Qualification and removal

Use a controlled test endpoint to exercise an outage and recovery and verify receipt at the intended mailbox. Never deliberately break an existing application's route to test monitoring. Verify certificate-expiry alerts with a controlled short-lived certificate. Local tests alone do not prove delivery to your mailbox.

To disable monitoring, run `sudo systemctl disable --now deployer-external-monitor.timer`. Retain the protected state for incident review; remove the SMTP credential only when this monitor is retired. Existing cluster alerting is unchanged by this installation.
