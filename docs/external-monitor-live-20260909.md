# Independent monitor activation, September 9

With the operator's explicit approval, the external monitor was installed on `deployer-pi-home` (`vanko1`) as a host-native systemd service. It checks `https://money.0xivanov.dev/readyz` every minute, requires HTTP 200 and verified TLS, and alerts when certificate lifetime drops below 14 days. Three consecutive failures trigger an alert, with four-hour repeats and one recovery notification.

The monitor connects directly to `smtp.gmail.com:587` using verified STARTTLS and the existing operator alert account. It does not route through the cluster SMTP relay or Alertmanager. Existing credentials were transferred through encrypted SSH connections without logging their contents or writing them to the Mac. The monitor directory is root-owned `0700`; configuration and password files are root-owned `0600`. No existing alert route was changed.

## Email rehearsal

`tests/external-monitor-email-drill.py` was executed once with explicit `--send-test-emails` authorization on the home Pi. It creates a private localhost TLS fixture, returning HTTP 503 and then HTTP 200. Trust for that synthetic certificate is restricted to the child test processes; normal system roots remain available for Gmail TLS. The production monitor configuration is not changed.

The normal monitor code observed the outage, sent an ALERT, observed recovery and sent RECOVERED. Both subjects include `TEST ONLY - legacy-vps monitor qualification`. Gmail accepted both messages and the persisted test state changed from notified to recovered. The temporary TLS fixture, test state and configuration were removed. The operator confirmed receipt of both test emails on September 9. This verifies end-to-end mailbox delivery for the controlled outage/recovery pair.

## Activation and limits

The real public endpoint passed the Pi's read-only check and initial normal service run. The one-minute timer is enabled. The monitor runs outside Kubernetes and continues independently of the customer cluster's monitoring services. It still depends on the home Pi, its Internet connection, public DNS and Gmail.

The first timer-triggered run completed successfully with exit status zero. Its private state reported zero consecutive failures and no outstanding alert, and the next run was scheduled one minute later. Systemd unit validation and all eleven local monitor tests passed.

The initial endpoint set covers the public application and its certificate. This does not monitor the management gRPC certificate, every hosted app, or the monitoring Pi's own availability. Customer-specific endpoints and recipients need their own configuration during onboarding. No app outage was induced; all three live Kubernetes nodes remained Ready and application deployments stayed available.
