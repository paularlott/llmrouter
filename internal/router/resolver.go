package router

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// dnsResolver implements scriptling.net.resolve.Resolver using Go's net package.
type dnsResolver struct {
	timeout time.Duration
}

var defaultNetResolver = &net.Resolver{}

func (r dnsResolver) LookupIP(host string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	return defaultNetResolver.LookupHost(ctx, host)
}

func (r dnsResolver) LookupSRV(service string) ([]*net.TCPAddr, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	_, srvs, err := defaultNetResolver.LookupSRV(ctx, "", "", service)
	if err != nil {
		return nil, err
	}
	var tcpAddrs []*net.TCPAddr
	for _, srv := range srvs {
		target := strings.TrimSuffix(srv.Target, ".")
		ips, err := defaultNetResolver.LookupHost(ctx, target)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			if parsed := net.ParseIP(ip); parsed != nil {
				tcpAddrs = append(tcpAddrs, &net.TCPAddr{IP: parsed, Port: int(srv.Port)})
			}
		}
	}
	if len(tcpAddrs) == 0 {
		return nil, errors.New("no addresses found")
	}
	return tcpAddrs, nil
}

func (r dnsResolver) ResolveSRVHttp(uri string) string {
	if !strings.HasPrefix(uri, "srv+") && !strings.HasPrefix(uri, "SRV+") {
		if !strings.HasPrefix(uri, "http://") && !strings.HasPrefix(uri, "https://") {
			return "https://" + uri
		}
		return uri
	}

	u, err := url.Parse(uri[4:])
	if err != nil {
		return uri[4:]
	}

	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	_, srvs, err := defaultNetResolver.LookupSRV(ctx, "", "", u.Host)
	if err != nil || len(srvs) == 0 {
		return uri[4:]
	}

	port := int(srvs[0].Port)
	if port <= 0 {
		return uri[4:]
	}

	u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(port))
	return u.String()
}
