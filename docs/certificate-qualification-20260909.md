# Certificate verification, September 9

## Management certificate

The VPS uses Certbot with the Cloudflare DNS authenticator for `deployer.0xivanov.dev`. Its timer is enabled, and the existing deployment hook restarts `deployer-server.service` after renewal. A simulated renewal against Let's Encrypt staging passed using:

```sh
sudo certbot renew --cert-name deployer.0xivanov.dev --dry-run \
  --server https://acme-staging-v02.api.letsencrypt.org/directory \
  --non-interactive --no-random-sleep-on-renew
```

The initial command was interrupted during Certbot's random sleep, before validation, and restarted with that delay disabled. No production certificate was replaced. The existing restart hook was then exercised separately against the current valid certificate. The server returned active/ready and all three Kubernetes nodes remained Ready. External TLS hostname/trust validation passed for the management endpoint; its certificate expires October 9 at 10:48:47 UTC.

## Application certificate

`tests/live-certificate-staging-smoke.py` creates a uniquely named temporary namespace with a staging Issuer and Certificate using the live issuer's HTTP-01/Traefik solver. It requires root on the known VPS and an explicit `--apply`. It refuses changed solver settings, checks staging issuance becomes Ready, and verifies that the production Certificate UID, revision and certificate bytes remain unchanged. Cleanup removes the namespace, staging account/certificate secrets and solver resources.

```sh
ssh deployer-vps sudo python3 - --apply < tests/live-certificate-staging-smoke.py
```

The final staging-only test passed. Earlier runs also issued staging certificates but failed their subsequent VPS-local HTTPS check, both via the public address and the local VPN ingress address. Those checks were separated from issuance verification rather than counted as successful. The cause of intermittent VPS-local ingress timeouts remains unresolved. Ten separate external HTTPS requests from the Mac all passed with the expected readiness body. This is sampled external availability evidence, not proof that the local routing problem is fixed.

A subsequent VPS-local check returned the expected HTTP 200 body through loopback, VPN and public addresses, with real hostname/TLS validation in every case. This confirms the earlier failures are intermittent; it does not establish their cause.

The production application certificate remains at revision 1, valid until October 10 at 10:30:27 UTC, with automatic renewal scheduled for September 10 at 10:30:27 UTC. Grafana's certificate remains Ready, valid until November 6, with renewal scheduled October 7. Staging issuance verifies the challenge path but does not prove that tomorrow's scheduled production renewal or Traefik's loading of a newly renewed certificate has completed.

## Independent alerts

The operator subsequently approved installation and test email delivery. The home Pi monitor is now enabled and Gmail accepted the controlled outage/recovery pair; see [activation evidence](external-monitor-live-20260909.md). The paragraph below records the state before that approval.

The external monitor's read-only check passed from the Mac against the application's public HTTPS readiness endpoint with a 14-day certificate-expiry threshold. Neither Pi currently has this independent monitor installed. Existing cluster email alerts remain configured; no email was sent by this rehearsal. Enabling the monitor on the home Pi and sending a labeled outage/recovery test pair to the existing operator address awaits the user's approval. No SMTP credentials were copied for that pending action.
