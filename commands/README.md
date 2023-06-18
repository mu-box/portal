[![portal logo](http://microbox.rocks/assets/readme-headers/portal.png)](http://microbox.cloud/open-source#portal)
[![Build Status](https://github.com/mu-box/portal/actions/workflows/ci.yaml/badge.svg)](https://github.com/mu-box/portal/actions)

# Portal

An api-driven, in-kernel layer 2/3 load balancer.

## CLI Commands:

```
portal - load balancer/proxy

Usage:
  portal [flags]
  portal [command]

Available Commands:
  add-service    Add service
  remove-service Remove service
  show-service   Show service
  show-services  Show all services
  set-services   Set service list
  set-service    Set service
  add-server     Add server to a service
  remove-server  Remove server from a service
  show-server    Show server on a service
  show-servers   Show all servers on a service
  set-servers    Set server list on a service
  add-route      Add route
  set-routes     Set route list
  show-routes    Show all routes
  remove-route   Remove route
  add-cert       Add cert
  set-certs      Set cert list
  show-certs     Show all certs
  remove-cert    Remove cert
  add-vip        Add vip
  set-vips       Set vip list
  show-vips      Show all vips
  remove-vip     Remove vip

Flags:
  -C, --api-cert="": SSL cert for the api
  -H, --api-host="127.0.0.1": Listen address for the API
  -k, --api-key="": SSL key for the api
  -p, --api-key-password="": Password for the SSL key
  -P, --api-port="8443": Listen address for the API
  -t, --api-token="": Token for API Access
  -b, --balancer="lvs": Load balancer to use (nginx|lvs)
  -r, --cluster-connection="none://": Cluster connection string (redis://127.0.0.1:6379)
  -T, --cluster-token="": Cluster security token
  -c, --conf="": Configuration file to load
  -d, --db-connection="scribble:///var/db/portal": Database connection string
  -i, --insecure[=false]: Disable tls key checking (client) and listen on http (server)
  -j, --just-proxy[=false]: Proxy only (no tcp/udp load balancing)
  -L, --log-file="": Log file to write to
  -l, --log-level="INFO": Log level to output
  -x, --proxy-http="0.0.0.0:80": Address to listen on for proxying http
  -X, --proxy-tls="0.0.0.0:443": Address to listen on for proxying https
  -s, --server[=false]: Run in server mode
  -v, --version[=false]: Print version info and exit
  -w, --work-dir="/var/db/portal": Directory for portal to use (balancer config)

Use "portal [command] --help" for more information about a command.
```

## Server Usage Example:
```
$ ./portal --server
```
or
```
$ ./portal -c config.json
```

>config.json
```json
{
  "api-token": "",
  "api-host": "127.0.0.1",
  "api-port": 8443,
  "api-key": "",
  "api-cert": "",
  "api-key-password": "",
  "db-connection": "scribble:///var/db/portal",
  "cluster-connection": "none://",
  "cluster-token": "",
  "insecure": false,
  "just-proxy": false,
  "proxy-http": "0.0.0.0:80",
  "proxy-tls": "0.0.0.0:443",
  "balancer": "nginx",
  "work-dir": "/var/db/portal",
  "log-level": "INFO",
  "log-file": "",
  "server": true
}
```

## Client Usage Example:

#### add service
```
$ ./portal add-service -O "127.0.0.3" -R 1234 -T "tcp" -s "rr" -e 0 -n ""
{"id":"tcp-127_0_0_3-1234","host":"127.0.0.3","port":1234,"type":"tcp","scheduler":"rr","persistence":0,"netmask":""}
$ ./portal add-service -j '{"id":"tcp-127_0_0_3-1234","host":"127.0.0.3","port":1234,"type":"tcp","scheduler":"rr","persistence":0,"netmask":"","servers":[{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1},{"id":"192_168_0_4-8080","host":"192.168.0.4","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}]}'
{"id":"tcp-127_0_0_3-1234","host":"127.0.0.3","port":1234,"type":"tcp","scheduler":"rr","persistence":0,"netmask":"","servers":[{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1},{"id":"192_168_0_4-8080","host":"192.168.0.4","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}]}
$ ./portal add-service -F "eth0" -R 1234 -T "tcp" -s "rr" -e 0 -n ""
{"id":"tcp-127_0_0_3-1234","host":"127.0.0.3","interface":"eth0","port":1234,"type":"tcp","scheduler":"rr","persistence":0,"netmask":""}
```

#### show services
```
$ ./portal show-services
[{"id":"tcp-127_0_0_3-1234","host":"127.0.0.3","port":1234,"type":"tcp","scheduler":"rr","persistence":0,"netmask":""}]
```

#### show service
```
$ ./portal show-service -I "tcp-127_0_0_3-1234"
{"id":"tcp-127_0_0_3-1234","host":"127.0.0.3","port":1234,"type":"tcp","scheduler":"rr","persistence":0,"netmask":""}
```

#### add server
```
$ ./portal add-server -I "tcp-127_0_0_3-1234" -o "192.168.0.3" -p 8080 -f "m" -w 5 -u 10 -l 1
{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}
$ ./portal add-server -I "tcp-127_0_0_3-1234" -j '{"host":"192.168.0.3", "port":8080, "forwarder": "m", "weight": 5, "upper_threshold": 10, "lower_threshold": 1}'
{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}
```

#### show servers
```
$ ./portal show-servers -O "127.0.0.3" -R "1234"
[{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}]
```

#### show server
```
$ ./portal show-server -I "tcp-127_0_0_3-1234" -S "192_168_0_3-8080"
{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}
```

#### remove server
```
$ ./portal remove-server -I "tcp-127_0_0_3-1234" -S "192_168_0_3-8080"
{"msg":"Success"}
```

#### show servers
```
$ ./portal show-servers -O "127.0.0.3" -R "1234"
[]
```

#### remove service
```
$ ./portal remove-service -I "tcp-127_0_0_3-1234"
{"msg":"Success"}
```

#### show services
```
$ ./portal show-services
[]
```

#### reset services
```
$ ./portal set-services -j '[{"id":"tcp-127_0_0_3-1234","host":"127.0.0.3","port":1234,"type":"tcp","scheduler":"rr","persistence":0,"netmask":"","servers":[{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1},{"id":"192_168_0_4-8080","host":"192.168.0.4","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}]}]'
[{"id":"tcp-127_0_0_3-1234","host":"127.0.0.3","port":1234,"type":"tcp","scheduler":"rr","persistence":0,"netmask":"","servers":[{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1},{"id":"192_168_0_4-8080","host":"192.168.0.4","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}]}]
```

#### reset servers
```
$ ./portal set-servers -I "tcp-127_0_0_3-1234" -j '[{"host":"192.168.0.3", "port":8080, "forwarder": "m", "weight": 5, "upper_threshold": 10, "lower_threshold": 1}]'
[{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}]
```

#### show services
```
$ ./portal show-services
[{"id":"tcp-127_0_0_3-1234","host":"127.0.0.3","port":1234,"type":"tcp","scheduler":"rr","persistence":0,"netmask":"","servers":[{"id":"192_168_0_3-8080","host":"192.168.0.3","port":8080,"forwarder":"m","weight":5,"upper_threshold":10,"lower_threshold":1}]}]
```

#### add route
```
$ ./portal add-route -j '{"domain":"portal.test", "page":"portal works\n"}'
{"subdomain":"","domain":"portal.test","path":"","targets":null,"fwdpath":"","page":"portal works\n"}
```

#### delete route
```
$ ./portal remove-route -d portal.test
{"msg":"Success"}
## OR
$ ./portal remove-route -j '{"domain":"portal.test"}'
{"msg":"Success"}
```

#### list routes
```
$ ./portal show-routes
[]
```

#### reset routes
```
$ ./portal set-routes -j '[{"domain":"portal.test", "page":"portal works\n"}]'
[{"subdomain":"","domain":"portal.test","path":"","targets":null,"fwdpath":"","page":"portal works\n"}]
```

#### add cert
```
$ ./portal add-cert -j '{"key":"-----BEGIN PRIVATE KEY-----\nMII.../J8\n-----END PRIVATE KEY-----",
  "cert":"-----BEGIN CERTIFICATE-----\nMII...aI=\n-----END CERTIFICATE-----"}'
{"key":"-----BEGIN PRIVATE KEY-----\nMII.../J8\n-----END PRIVATE KEY-----", "cert":"-----BEGIN CERTIFICATE-----\nMII...aI=\n-----END CERTIFICATE-----"}
```

#### delete cert
```
$ ./portal remove-cert -j '{"key":"-----BEGIN PRIVATE KEY-----\nMII.../J8\n-----END PRIVATE KEY-----",
            "cert":"-----BEGIN CERTIFICATE-----\nMII...aI=\n-----END CERTIFICATE-----"}'
{"msg":"Success"}
```

#### list certs
```
$ ./portal show-certs
[]
```

#### reset certs
```
$ ./portal set-certs -j '[{"key":"-----BEGIN PRIVATE KEY-----\nMII.../J8\n-----END PRIVATE KEY-----",
  "cert":"-----BEGIN CERTIFICATE-----\nMII...aI=\n-----END CERTIFICATE-----"}]'
[{"key":"-----BEGIN PRIVATE KEY-----\nMII.../J8\n-----END PRIVATE KEY-----", "cert":"-----BEGIN CERTIFICATE-----\nMII...aI=\n-----END CERTIFICATE-----"}]
```

#### add vip
```
$ ./portal add-vip -i -A eth0:1 -F eth0 -I 192.168.0.100
{"ip":"192.168.0.100","interface":"eth0","alias":"eth0:1"}
```

#### delete vip
```
$ ./portal remove-vip -i -F eth0 -I 192.168.0.100
{"msg":"Success"}
```

#### list vips
```
$ ./portal show-vips
[]
```

#### reset vips
```
$ ./portal set-vips -j '[{"ip":"192.168.0.100","interface":"eth0","alias":"eth0:1"}]'
[{"ip":"192.168.0.100","interface":"eth0","alias":"eth0:1"}]
```

[![portal logo](http://microbox.rocks/assets/open-src/microbox-open-src.png)](http://microbox.cloud/open-source)
