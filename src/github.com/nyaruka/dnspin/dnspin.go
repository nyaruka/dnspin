package main

import (
	"log"
	"github.com/miekg/dns"
	"errors"
	"os"
	"bufio"
	"fmt"
	"strings"
	"io/ioutil"
	"time"
)

type host_config struct {
	hostname    string
	dns_server  string
	ip_address  string
}

const NIL = "NIL"
const ERROR = "ERROR"
const MISSING = "MISSING"

const DNSPIN_BEGIN    = "### DNSPIN BEGIN ###"
const DNSPIN_END      = "### DNSPIN END #####"

const PRE_PIN  = 0
const IN_PIN   = 1
const POST_PIN = 2

func lookupIP(host string, server string) (string, error) {
	c := dns.Client{}
	m := dns.Msg{}
	m.SetQuestion(dns.Fqdn(host), dns.TypeA)
	r, _, err := c.Exchange(&m, server+":53")
	if err != nil {
		return "", err
	}
	for _, ans := range r.Answer {
		if a, ok := ans.(*dns.A); ok {
			return a.A.String(), nil
		}
	}

	// we reached the server and it has no record
	return MISSING, nil
}

func loadHostConfig(filename string) (hosts []*host_config, err error){
	hosts = make([]*host_config, 0, 5)

	f, err := os.Open(filename)
	if err != nil {
		return hosts, err
	}
	defer f.Close()

	lineno := 0

	// scan the file line by line
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lineno += 1
		line := scanner.Text()

		if len(line) > 0 && !strings.HasPrefix(line, "#") {
			// now split our line into two parts, hostname and dns server
			fields := strings.Fields(line)
			if len(fields) != 2 {
				return hosts, errors.New(fmt.Sprintf("Unexpected input on line %d: %s", lineno, line))
			}

			// save away to our config
			hosts = append(hosts, &host_config{fields[0], fields[1], NIL})
		}
	}

	return hosts, nil
}

func writeHostsFile(hosts []*host_config) (wrote bool, err error) {
	// first read in our current hosts file
	in, err := os.Open("/etc/hosts")
	if err != nil {
		return false, err
	}
	defer in.Close()

	pre_lines  := make([]string, 0, 10)
	pin_lines  := make([]string, 0, 10)
	post_lines := make([]string, 0, 10)

	location := PRE_PIN

	scanner := bufio.NewScanner(in)
	for scanner.Scan() {
		line := scanner.Text()
		if (line == DNSPIN_BEGIN) {
			location = IN_PIN
		} else if (line == DNSPIN_END){
			location = POST_PIN
		} else {
			if (location == PRE_PIN) {
				pre_lines = append(pre_lines, line)
			} else if (location == IN_PIN) {
				if !strings.HasPrefix(line, "#") {
					pin_lines = append(pin_lines, line)
				}
			} else if (location == POST_PIN) {
				pin_lines = append(post_lines, line)
			}
		}
	}

	// parse our current mappings
	current_mappings := make(map[string]string)
	for _, line := range(pin_lines) {
		fields := strings.Fields(line)

		// if this line is a host mapping, save it
		if len(fields) == 2 {
			current_mappings[fields[1]] = fields[0]
		}
	}

	// are there any changes? to be made
	needs_rewrite := len(current_mappings) != len(hosts)
	for _, host := range(hosts){
		ip_address, exists := current_mappings[host.hostname]
		if !exists || ip_address != host.ip_address {
			needs_rewrite = true
			break
		}
	}

	// no rewrite needed, return
	if !needs_rewrite {
		return false, nil
	}

	// ok, rewrite our hosts file to a tmp file first
	out, err := ioutil.TempFile("/tmp", "hosts")
	if err != nil {
		return false, err
	}
	defer out.Close()
	defer os.Remove(out.Name())

	err = out.Chmod(0644)
	if err != nil {
		return false, err
	}

	w := bufio.NewWriter(out)

	// first write lines before our block
	for _, line := range(pre_lines) {
		fmt.Fprintln(w, line)
	}

	// start our block
	fmt.Fprintln(w, DNSPIN_BEGIN)

	// write our entries
	for _, host := range(hosts){
		// we had trouble looking this up, use the old one if it exists
		if host.ip_address == ERROR {
			ip_address, exists := current_mappings[host.hostname]
			if exists {
				fmt.Fprintf(w, "# %s: cached value, error during lookup to %s\n", host.hostname, host.dns_server)
				fmt.Fprintf(w, "%s\t%s\n", ip_address, host.hostname)
			} else {
				fmt.Fprintf(w, "# %s: error during lookup to %s\n", host.hostname, host.dns_server)
			}
		} else if host.ip_address != MISSING {
			fmt.Fprintf(w, "%s\t%s\n", host.ip_address, host.hostname)
		}
	}

	// end our block
	fmt.Fprintln(w, DNSPIN_END)

	// write our post block
	for _, line := range(post_lines) {
		fmt.Fprintln(w, line)
	}
	err = w.Flush()
	if err != nil {
		return false, err
	}

	// move it atomically over our /etc/hosts file
	err = os.Rename(out.Name(), "/etc/hosts")
	if err != nil {
		return false, err
	}

	return true, err
}

func main() {
	hosts, err := loadHostConfig("dnspin.conf")
	if err != nil {
		log.Fatalf("Error loading dnspin.conf: %v", err)
	}

	for {
		for _, host := range (hosts) {
			ip, err := lookupIP(host.hostname, host.dns_server)
			if err != nil {
				log.Printf("Error: %s", err)
				host.ip_address = ERROR
			} else {
				host.ip_address = ip
			}
			log.Printf("%s = %s", host.hostname, host.ip_address)
		}

		// rewrite our hosts file
		wrote, err := writeHostsFile(hosts)
		if err != nil {
			log.Printf("Error writing hosts file: %v", err)
		} else {
			if wrote {
				log.Printf("Hosts file updated")
			} else {
				log.Printf("No changes, hosts file not updated")
			}
		}

		// sleep 5 seconds then start all over
		time.Sleep(5 * time.Second)
	}
}

