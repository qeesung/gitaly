# Configuring Praefect

This document describes how to configure the praefect server.

Praefect is configured via a [TOML](https://github.com/toml-lang/toml)
configuration file. The TOML file contents and location depends on how you
installed GitLab. See: https://docs.gitlab.com/ce/administration/gitaly/

The configuration file is passed as an argument to the `praefect`
executable. This is usually done by either omnibus-gitlab or your init
script.

```
praefect -config /path/to/config.toml
```

## Format

```toml
listen_addr = "127.0.0.1:2305"
socket_path = "/path/to/praefect.socket"
tls_listen_addr = "127.0.0.1:2306"

[tls]
certificate_path = '/home/git/cert.cert'
key_path = '/home/git/key.pem'

[logging]
format = "json"
level = "info"

[[virtual_storage]]
name = 'praefect'

[[virtual_storage.node]]
  storage = "gitaly-0"
  address = "tcp://gitaly-0.internal"
  token = 'secret_token'
```

An example [config toml](../../config.praefect.toml.example) is stored in this repository.
