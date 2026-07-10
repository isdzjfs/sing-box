### Structure

```json
{
  "type": "shadowsocksr",
  "tag": "ssr-out",

  "server": "127.0.0.1",
  "server_port": 1080,
  "method": "aes-256-cfb",
  "password": "password",
  "obfs": "plain",
  "obfs_param": "",
  "protocol": "origin",
  "protocol_param": "",
  "network": "tcp",

  ... // Dial Fields
}
```

### Fields

#### server

==Required==

The server address.

#### server_port

==Required==

The server port.

#### method

==Required==

Encryption methods:

* `none`
* `aes-128-ctr`
* `aes-192-ctr`
* `aes-256-ctr`
* `aes-128-cfb`
* `aes-192-cfb`
* `aes-256-cfb`
* `rc4-md5`
* `chacha20`
* `chacha20-ietf`
* `xchacha20`

#### password

==Required==

The ShadowsocksR password.

#### obfs

Obfuscation method.

Defaults to `plain`.

Available values:

* `plain`
* `http_simple`
* `http_post`
* `random_head`
* `tls1.2_ticket_auth`
* `tls1.2_ticket_fastauth`

#### obfs_param

Obfuscation parameter.

#### protocol

SSR protocol.

Defaults to `origin`.

Available values:

* `origin`
* `auth_sha1_v4`
* `auth_aes128_md5`
* `auth_aes128_sha1`
* `auth_chain_a`
* `auth_chain_b`

#### protocol_param

Protocol parameter.

#### network

Enabled network.

One of `tcp` `udp`.

Both are enabled by default.

### Dial Fields

See [Dial Fields](/configuration/shared/dial/) for details.
