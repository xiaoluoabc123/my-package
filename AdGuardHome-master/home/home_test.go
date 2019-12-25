package home

import (
	"context"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/AdguardTeam/dnsproxy/upstream"
	"github.com/stretchr/testify/assert"
)

const yamlConf = `bind_host: 127.0.0.1
bind_port: 3000
users: []
language: en
rlimit_nofile: 0
web_session_ttl: 720
dns:
  bind_host: 127.0.0.1
  port: 5354
  statistics_interval: 90
  querylog_enabled: true
  querylog_interval: 90
  querylog_memsize: 0
  protection_enabled: true
  blocking_mode: null_ip
  blocked_response_ttl: 0
  ratelimit: 100
  ratelimit_whitelist: []
  refuse_any: false
  bootstrap_dns:
  - 1.1.1.1:53
  all_servers: false
  allowed_clients: []
  disallowed_clients: []
  blocked_hosts: []
  parental_block_host: family-block.dns.adguard.com
  safebrowsing_block_host: standard-block.dns.adguard.com
  cache_size: 0
  upstream_dns:
  - https://1.1.1.1/dns-query
  filtering_enabled: true
  filters_update_interval: 168
  parental_sensitivity: 13
  parental_enabled: true
  safesearch_enabled: false
  safebrowsing_enabled: false
  safebrowsing_cache_size: 1048576
  safesearch_cache_size: 1048576
  parental_cache_size: 1048576
  cache_time: 30
  rewrites: []
  blocked_services: []
tls:
  enabled: false
  server_name: www.example.com
  force_https: false
  port_https: 443
  port_dns_over_tls: 853
  certificate_chain: ""
  private_key: ""
  certificate_path: ""
  private_key_path: ""
filters:
- enabled: true
  url: https://adguardteam.github.io/AdGuardSDNSFilter/Filters/filter.txt
  name: AdGuard Simplified Domain Names filter
  id: 1
- enabled: false
  url: https://hosts-file.net/ad_servers.txt
  name: hpHosts - Ad and Tracking servers only
  id: 2
- enabled: false
  url: https://adaway.org/hosts.txt
  name: adaway
  id: 3
user_rules:
- ""
dhcp:
  enabled: false
  interface_name: ""
  gateway_ip: ""
  subnet_mask: ""
  range_start: ""
  range_end: ""
  lease_duration: 86400
  icmp_timeout_msec: 1000
clients: []
log_file: ""
verbose: false
schema_version: 5
`

// . Create a configuration file
// . Start AGH instance
// . Check Web server
// . Check DNS server
// . Wait until the filters are downloaded
// . Stop and cleanup
func TestHome(t *testing.T) {
	dir := prepareTestDir()
	defer func() { _ = os.RemoveAll(dir) }()
	fn := filepath.Join(dir, "AdGuardHome.yaml")

	assert.True(t, ioutil.WriteFile(fn, []byte(yamlConf), 0644) == nil)
	fn, _ = filepath.Abs(fn)

	args := options{}
	args.configFilename = fn
	args.workDir = dir
	go run(args)

	var err error
	var resp *http.Response
	h := http.Client{}
	for i := 0; i != 5; i++ {
		resp, err = h.Get("http://127.0.0.1:3000/")
		if err == nil && resp.StatusCode != 404 {
			break
		}
		time.Sleep(1 * time.Second)
	}
	assert.Truef(t, err == nil, "%s", err)
	assert.Equal(t, 200, resp.StatusCode)

	resp, err = h.Get("http://127.0.0.1:3000/control/status")
	assert.Truef(t, err == nil, "%s", err)
	assert.Equal(t, 200, resp.StatusCode)

	r := upstream.NewResolver("127.0.0.1:5354", 3*time.Second)
	addrs, err := r.LookupIPAddr(context.TODO(), "static.adguard.com")
	assert.Truef(t, err == nil, "%s", err)
	haveIP := len(addrs) != 0
	assert.True(t, haveIP)

	for i := 1; ; i++ {
		st, err := os.Stat(filepath.Join(dir, "data", "filters", "1.txt"))
		if err == nil && st.Size() != 0 {
			break
		}
		if i == 5 {
			assert.True(t, false)
			break
		}
		time.Sleep(1 * time.Second)
	}

	cleanup()
	cleanupAlways()
}
