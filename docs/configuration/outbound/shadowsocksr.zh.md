### 结构

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

  ... // 拨号字段
}
```

### 字段

#### server

==必填==

服务器地址。

#### server_port

==必填==

服务器端口。

#### method

==必填==

加密方法：

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

==必填==

ShadowsocksR 密码。

#### obfs

混淆方法。

默认使用 `plain`。

可用值：

* `plain`
* `http_simple`
* `http_post`
* `random_head`
* `tls1.2_ticket_auth`
* `tls1.2_ticket_fastauth`

#### obfs_param

混淆参数。

#### protocol

SSR 协议。

默认使用 `origin`。

可用值：

* `origin`
* `auth_sha1_v4`
* `auth_aes128_md5`
* `auth_aes128_sha1`
* `auth_chain_a`
* `auth_chain_b`

#### protocol_param

协议参数。

#### network

启用的网络协议。

`tcp` 或 `udp`。

默认所有。

### 拨号字段

参阅 [拨号字段](/zh/configuration/shared/dial/)。
