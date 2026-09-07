# CLI contexts

The deployer CLI can keep access to multiple independent customer control planes in one local config file. A context contains its server endpoint, a credential reference, an optional environment ID, and an operator-facing customer label.

Existing config files with `server_url` and `admin_token` continue to work as the legacy default. They are stored with mode `0600` under the existing config path. A legacy login updates only that default and preserves named contexts.

Create or update a named context by logging in with `--context`:

```sh
deployer --config ~/.config/deployer/config.json \
  --context customer-a --token dep_admin_a \
  login https://customer-a.example:7443
```

The token is stored in the protected local config file for compatibility with the existing login flow. Contexts can also use `credential_ref` values of `env:VARIABLE` or `file:/path/to/token`; credential files must be readable only by the current user. Tokens are never printed by context commands.

Inspect or select the current context:

```sh
deployer contexts list
deployer contexts use customer-a
deployer --context customer-b apps list
```

Selection precedence for operational commands is: `--context`, `DEPLOYER_CONTEXT`, the saved current context, then the legacy endpoint and token. Within a selected context, `--token` can override that context's credential while `--server` is rejected, so a credential cannot be sent to a different endpoint. Legacy `--server` and `--token` flags retain their existing precedence over the saved config. Unknown contexts fail before a client is created.

Human-readable mutating command output identifies the selected context. JSON output retains the existing response shape so scripts remain compatible.

The current protobuf API has no capability-negotiated environment identity field. Identity verification needs an additive protocol change before it can become an enforced server identity check.

Each customer environment still requires separate server credentials, certificates, network ranges, backup locations, resource budgets, and pinned versions. Billing and customer provisioning remain operator processes outside the deployer CLI.

Use `deployer contexts use --legacy` to return to the original single-endpoint configuration. A legacy `login` also selects that configuration; named login preserves it and other customer contexts. Named credential rotation preserves existing customer labels and environment IDs unless explicitly replaced.

Server identity binding is not implemented. A context with a nonempty `server_identity` fails closed until verification is available. HTTPS authenticates the configured hostname; the customer and environment labels are operator metadata, not a verified server identity.
