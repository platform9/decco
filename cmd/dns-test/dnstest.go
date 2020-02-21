package main

import (
	"flag"
	"fmt"

	"github.com/sirupsen/logrus"
	"k8s.io/kubernetes/federation/pkg/dnsprovider"
	_ "k8s.io/kubernetes/federation/pkg/dnsprovider/providers/aws/route53"
	"k8s.io/kubernetes/federation/pkg/dnsprovider/rrstype"
)

func main() {
	ipPtr := flag.String("ip", "192.168.0.1", "the IP address (default: '192.168.0.1')")
	prefixPtr := flag.String("prefix", "dns-test-1", "the fqdn prefix (default: 'host-1')")
	zonePtr := flag.String("zone", "platform9.horse.", "the zone name (default: 'platform9.horse.')")
	flag.Parse()
	err := updateDns(*ipPtr, *prefixPtr, *zonePtr)
	if err != nil {
		logrus.Errorf("top level error: %s", err)
	} else {
		fmt.Println("dns update successful.")
	}
}

func updateDns(ip string, name string, zoneName string) error {
	dnsProvider, err := dnsprovider.GetDnsProvider("aws-route53", nil)
	if err != nil {
		logrus.Panicf("failed to get dns provider: %s", err)
	}
	zonesApi, _ := dnsProvider.Zones()
	zones, err := zonesApi.List()
	if err != nil {
		return fmt.Errorf("failed to list dns zones: %s", err)
	}
	for _, zone := range zones {
		if zone.Name() == zoneName {
			rrName := fmt.Sprintf("%s.%s", name, zoneName)
			logrus.Infof("found zone %s, looking for %s record",
				zoneName, rrName)
			rrSets, _ := zone.ResourceRecordSets()
			rrSetList, err := rrSets.Get(rrName)
			if err != nil {
				return fmt.Errorf("failed to lookup record name %s: %s",
					rrName, err)
			}
			changeSet := rrSets.StartChangeset()
			var action string
			rrSet := rrSets.New(rrName, []string{ip}, 180, rrstype.A)
			if len(rrSetList) == 0 {
				action = "creation"
			} else {
				action = "update"
				changeSet.Remove(rrSetList[0])
			}
			changeSet.Add(rrSet)
			err = changeSet.Apply()
			if err != nil {
				return fmt.Errorf("%s of record set %s failed: %s",
					action, rrName, err)
			}
			logrus.Infof("%s of record set %s with ip %s succeeded",
				action, rrName, ip)
			return nil
		}
	}
	return fmt.Errorf("failed to find DNS zone %s", zoneName)
}
