package home

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/AdguardTeam/AdGuardHome/dhcpd"
	"github.com/AdguardTeam/AdGuardHome/dnsforward"
	"github.com/AdguardTeam/golibs/log"
	"github.com/AdguardTeam/golibs/utils"
)

const (
	clientsUpdatePeriod = 1 * time.Hour
)

// Client information
type Client struct {
	IDs                 []string
	Name                string
	UseOwnSettings      bool // false: use global settings
	FilteringEnabled    bool
	SafeSearchEnabled   bool
	SafeBrowsingEnabled bool
	ParentalEnabled     bool
	WhoisInfo           [][]string // [[key,value], ...]

	UseOwnBlockedServices bool // false: use global settings
	BlockedServices       []string

	Upstreams []string // list of upstream servers to be used for the client's requests
}

type clientSource uint

// Client sources
const (
	// Priority: etc/hosts > DHCP > ARP > rDNS > WHOIS
	ClientSourceWHOIS     clientSource = iota // from WHOIS
	ClientSourceRDNS                          // from rDNS
	ClientSourceDHCP                          // from DHCP
	ClientSourceARP                           // from 'arp -a'
	ClientSourceHostsFile                     // from /etc/hosts
)

// ClientHost information
type ClientHost struct {
	Host      string
	Source    clientSource
	WhoisInfo [][]string // [[key,value], ...]
}

type clientsContainer struct {
	list    map[string]*Client     // name -> client
	idIndex map[string]*Client     // IP -> client
	ipHost  map[string]*ClientHost // IP -> Hostname
	lock    sync.Mutex

	dhcpServer *dhcpd.Server

	testing bool // if TRUE, this object is used for internal tests
}

// Init initializes clients container
// Note: this function must be called only once
func (clients *clientsContainer) Init(objects []clientObject, dhcpServer *dhcpd.Server) {
	if clients.list != nil {
		log.Fatal("clients.list != nil")
	}
	clients.list = make(map[string]*Client)
	clients.idIndex = make(map[string]*Client)
	clients.ipHost = make(map[string]*ClientHost)
	clients.dhcpServer = dhcpServer
	clients.addFromConfig(objects)

	if !clients.testing {
		go clients.periodicUpdate()

		clients.registerWebHandlers()
	}
}

type clientObject struct {
	Name                string   `yaml:"name"`
	IDs                 []string `yaml:"ids"`
	UseGlobalSettings   bool     `yaml:"use_global_settings"`
	FilteringEnabled    bool     `yaml:"filtering_enabled"`
	ParentalEnabled     bool     `yaml:"parental_enabled"`
	SafeSearchEnabled   bool     `yaml:"safebrowsing_enabled"`
	SafeBrowsingEnabled bool     `yaml:"safesearch_enabled"`

	UseGlobalBlockedServices bool     `yaml:"use_global_blocked_services"`
	BlockedServices          []string `yaml:"blocked_services"`

	Upstreams []string `yaml:"upstreams"`
}

func (clients *clientsContainer) addFromConfig(objects []clientObject) {
	for _, cy := range objects {
		cli := Client{
			Name:                cy.Name,
			IDs:                 cy.IDs,
			UseOwnSettings:      !cy.UseGlobalSettings,
			FilteringEnabled:    cy.FilteringEnabled,
			ParentalEnabled:     cy.ParentalEnabled,
			SafeSearchEnabled:   cy.SafeSearchEnabled,
			SafeBrowsingEnabled: cy.SafeBrowsingEnabled,

			UseOwnBlockedServices: !cy.UseGlobalBlockedServices,
			BlockedServices:       cy.BlockedServices,

			Upstreams: cy.Upstreams,
		}
		_, err := clients.Add(cli)
		if err != nil {
			log.Tracef("clientAdd: %s", err)
		}
	}
}

// WriteDiskConfig - write configuration
func (clients *clientsContainer) WriteDiskConfig(objects *[]clientObject) {
	clients.lock.Lock()
	for _, cli := range clients.list {
		cy := clientObject{
			Name:                     cli.Name,
			UseGlobalSettings:        !cli.UseOwnSettings,
			FilteringEnabled:         cli.FilteringEnabled,
			ParentalEnabled:          cli.ParentalEnabled,
			SafeSearchEnabled:        cli.SafeSearchEnabled,
			SafeBrowsingEnabled:      cli.SafeBrowsingEnabled,
			UseGlobalBlockedServices: !cli.UseOwnBlockedServices,
		}

		cy.IDs = make([]string, len(cli.IDs))
		copy(cy.IDs, cli.IDs)

		cy.BlockedServices = make([]string, len(cli.BlockedServices))
		copy(cy.BlockedServices, cli.BlockedServices)

		cy.Upstreams = make([]string, len(cli.Upstreams))
		copy(cy.Upstreams, cli.Upstreams)

		*objects = append(*objects, cy)
	}
	clients.lock.Unlock()
}

func (clients *clientsContainer) periodicUpdate() {
	for {
		clients.addFromHostsFile()
		clients.addFromSystemARP()
		clients.addFromDHCP()
		time.Sleep(clientsUpdatePeriod)
	}
}

// Exists checks if client with this IP already exists
func (clients *clientsContainer) Exists(ip string, source clientSource) bool {
	clients.lock.Lock()
	defer clients.lock.Unlock()

	_, ok := clients.idIndex[ip]
	if ok {
		return true
	}

	ch, ok := clients.ipHost[ip]
	if !ok {
		return false
	}
	if source > ch.Source {
		return false // we're going to overwrite this client's info with a stronger source
	}
	return true
}

// Find searches for a client by IP
func (clients *clientsContainer) Find(ip string) (Client, bool) {
	ipAddr := net.ParseIP(ip)
	if ipAddr == nil {
		return Client{}, false
	}

	clients.lock.Lock()
	defer clients.lock.Unlock()

	c, ok := clients.idIndex[ip]
	if ok {
		return *c, true
	}

	for _, c = range clients.list {
		for _, id := range c.IDs {
			_, ipnet, err := net.ParseCIDR(id)
			if err != nil {
				continue
			}
			if ipnet.Contains(ipAddr) {
				return *c, true
			}
		}
	}

	if clients.dhcpServer == nil {
		return Client{}, false
	}
	macFound := clients.dhcpServer.FindMACbyIP(ipAddr)
	if macFound == nil {
		return Client{}, false
	}
	for _, c = range clients.list {
		for _, id := range c.IDs {
			hwAddr, err := net.ParseMAC(id)
			if err != nil {
				continue
			}
			if bytes.Equal(hwAddr, macFound) {
				return *c, true
			}
		}
	}

	return Client{}, false
}

// FindAutoClient - search for an auto-client by IP
func (clients *clientsContainer) FindAutoClient(ip string) (ClientHost, bool) {
	ipAddr := net.ParseIP(ip)
	if ipAddr == nil {
		return ClientHost{}, false
	}

	clients.lock.Lock()
	defer clients.lock.Unlock()

	ch, ok := clients.ipHost[ip]
	if ok {
		return *ch, true
	}
	return ClientHost{}, false
}

// Check if Client object's fields are correct
func (c *Client) check() error {
	if len(c.Name) == 0 {
		return fmt.Errorf("Invalid Name")
	}

	if len(c.IDs) == 0 {
		return fmt.Errorf("ID required")
	}

	for i, id := range c.IDs {
		ip := net.ParseIP(id)
		if ip != nil {
			c.IDs[i] = ip.String() // normalize IP address
			continue
		}

		_, _, err := net.ParseCIDR(id)
		if err == nil {
			continue
		}

		_, err = net.ParseMAC(id)
		if err == nil {
			continue
		}

		return fmt.Errorf("Invalid ID: %s", id)
	}

	if len(c.Upstreams) != 0 {
		err := dnsforward.ValidateUpstreams(c.Upstreams)
		if err != nil {
			return fmt.Errorf("Invalid upstream servers: %s", err)
		}
	}

	return nil
}

// Add a new client object
// Return true: success;  false: client exists.
func (clients *clientsContainer) Add(c Client) (bool, error) {
	e := c.check()
	if e != nil {
		return false, e
	}

	clients.lock.Lock()
	defer clients.lock.Unlock()

	// check Name index
	_, ok := clients.list[c.Name]
	if ok {
		return false, nil
	}

	// check ID index
	for _, id := range c.IDs {
		c2, ok := clients.idIndex[id]
		if ok {
			return false, fmt.Errorf("Another client uses the same ID (%s): %s", id, c2.Name)
		}
	}

	// remove auto-clients with the same IP address, keeping WHOIS info if possible
	for _, id := range c.IDs {
		ch, ok := clients.ipHost[id]
		if ok {
			if len(c.WhoisInfo) == 0 {
				c.WhoisInfo = ch.WhoisInfo
			}
			delete(clients.ipHost, id)
		}
	}

	// update Name index
	clients.list[c.Name] = &c

	// update ID index
	for _, id := range c.IDs {
		clients.idIndex[id] = &c
	}

	log.Tracef("'%s': ID:%v [%d]", c.Name, c.IDs, len(clients.list))
	return true, nil
}

// Del removes a client
func (clients *clientsContainer) Del(name string) bool {
	clients.lock.Lock()
	defer clients.lock.Unlock()

	c, ok := clients.list[name]
	if !ok {
		return false
	}

	// update Name index
	delete(clients.list, name)

	// update ID index
	for _, id := range c.IDs {
		delete(clients.idIndex, id)
	}
	return true
}

// Return TRUE if arrays are equal
func arraysEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i != len(a); i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Update a client
func (clients *clientsContainer) Update(name string, c Client) error {
	err := c.check()
	if err != nil {
		return err
	}

	clients.lock.Lock()
	defer clients.lock.Unlock()

	old, ok := clients.list[name]
	if !ok {
		return fmt.Errorf("Client not found")
	}

	// check Name index
	if old.Name != c.Name {
		_, ok = clients.list[c.Name]
		if ok {
			return fmt.Errorf("Client already exists")
		}
	}

	// check IP index
	if !arraysEqual(old.IDs, c.IDs) {
		for _, id := range c.IDs {
			c2, ok := clients.idIndex[id]
			if ok && c2 != old {
				return fmt.Errorf("Another client uses the same ID (%s): %s", id, c2.Name)
			}
		}

		// update ID index
		for _, id := range old.IDs {
			delete(clients.idIndex, id)
		}
		for _, id := range c.IDs {
			clients.idIndex[id] = old
		}
	}

	// update Name index
	if old.Name != c.Name {
		delete(clients.list, old.Name)
		clients.list[c.Name] = old
	}

	*old = c
	return nil
}

// SetWhoisInfo - associate WHOIS information with a client
func (clients *clientsContainer) SetWhoisInfo(ip string, info [][]string) {
	clients.lock.Lock()
	defer clients.lock.Unlock()

	c, ok := clients.idIndex[ip]
	if ok {
		c.WhoisInfo = info
		log.Debug("Clients: set WHOIS info for client %s: %v", c.Name, c.WhoisInfo)
		return
	}

	ch, ok := clients.ipHost[ip]
	if ok {
		ch.WhoisInfo = info
		log.Debug("Clients: set WHOIS info for auto-client %s: %v", ch.Host, ch.WhoisInfo)
		return
	}

	ch = &ClientHost{
		Source: ClientSourceWHOIS,
	}
	ch.WhoisInfo = info
	clients.ipHost[ip] = ch
	log.Debug("Clients: set WHOIS info for auto-client with IP %s: %v", ip, ch.WhoisInfo)
}

// AddHost adds new IP -> Host pair
// Use priority of the source (etc/hosts > ARP > rDNS)
//  so we overwrite existing entries with an equal or higher priority
func (clients *clientsContainer) AddHost(ip, host string, source clientSource) (bool, error) {
	clients.lock.Lock()
	defer clients.lock.Unlock()

	// check index
	_, ok := clients.idIndex[ip]
	if ok {
		return false, nil
	}

	// check auto-clients index
	ch, ok := clients.ipHost[ip]
	if ok && ch.Source > source {
		return false, nil
	} else if ok {
		ch.Source = source
	} else {
		ch = &ClientHost{
			Host:   host,
			Source: source,
		}
		clients.ipHost[ip] = ch
	}
	log.Tracef("'%s' -> '%s' [%d]", ip, host, len(clients.ipHost))
	return true, nil
}

// Parse system 'hosts' file and fill clients array
func (clients *clientsContainer) addFromHostsFile() {
	hostsFn := "/etc/hosts"
	if runtime.GOOS == "windows" {
		hostsFn = os.ExpandEnv("$SystemRoot\\system32\\drivers\\etc\\hosts")
	}

	d, e := ioutil.ReadFile(hostsFn)
	if e != nil {
		log.Info("Can't read file %s: %v", hostsFn, e)
		return
	}

	lines := strings.Split(string(d), "\n")
	n := 0
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if len(ln) == 0 || ln[0] == '#' {
			continue
		}

		fields := strings.Fields(ln)
		if len(fields) < 2 {
			continue
		}

		ok, e := clients.AddHost(fields[0], fields[1], ClientSourceHostsFile)
		if e != nil {
			log.Tracef("%s", e)
		}
		if ok {
			n++
		}
	}

	log.Debug("Added %d client aliases from %s", n, hostsFn)
}

// Add IP -> Host pairs from the system's `arp -a` command output
// The command's output is:
// HOST (IP) at MAC on IFACE
func (clients *clientsContainer) addFromSystemARP() {

	if runtime.GOOS == "windows" {
		return
	}

	cmd := exec.Command("arp", "-a")
	log.Tracef("executing %s %v", cmd.Path, cmd.Args)
	data, err := cmd.Output()
	if err != nil || cmd.ProcessState.ExitCode() != 0 {
		log.Debug("command %s has failed: %v code:%d",
			cmd.Path, err, cmd.ProcessState.ExitCode())
		return
	}

	n := 0
	lines := strings.Split(string(data), "\n")
	for _, ln := range lines {

		open := strings.Index(ln, " (")
		close := strings.Index(ln, ") ")
		if open == -1 || close == -1 || open >= close {
			continue
		}

		host := ln[:open]
		ip := ln[open+2 : close]
		if utils.IsValidHostname(host) != nil || net.ParseIP(ip) == nil {
			continue
		}

		ok, e := clients.AddHost(ip, host, ClientSourceARP)
		if e != nil {
			log.Tracef("%s", e)
		}
		if ok {
			n++
		}
	}

	log.Debug("Added %d client aliases from 'arp -a' command output", n)
}

// add clients from DHCP that have non-empty Hostname property
func (clients *clientsContainer) addFromDHCP() {
	if clients.dhcpServer == nil {
		return
	}
	leases := clients.dhcpServer.Leases()
	n := 0
	for _, l := range leases {
		if len(l.Hostname) == 0 {
			continue
		}
		ok, _ := clients.AddHost(l.IP.String(), l.Hostname, ClientSourceDHCP)
		if ok {
			n++
		}
	}
	log.Debug("Added %d client aliases from DHCP", n)
}
