package dns

import (
	"github.com/sirupsen/logrus"
	_ "k8s.io/kubernetes/federation/pkg/dnsprovider/providers/aws/route53"
	"k8s.io/kubernetes/federation/pkg/dnsprovider"
	"k8s.io/kubernetes/federation/pkg/dnsprovider/rrstype"
	"os"
	"fmt"
)

var (
	dnsProvider dnsprovider.Interface
	log = logrus.WithField("pkg","dns")
)

func init () {
	dnsProviderName := os.Getenv("DNS_PROVIDER_NAME")
	if len(dnsProviderName) > 0 {
		var err error
		dnsProvider, err = dnsprovider.GetDnsProvider(dnsProviderName, nil)
		if err != nil {
			logrus.Panicf("failed to get dns provider %s", dnsProviderName)
		}
	}
}

func Enabled() bool {
	return dnsProvider != nil
}

func UpdateRecord(domainName string, name string, ip string, delete bool) error {
	if dnsProvider == nil {
		return fmt.Errorf("DNS provider not enabled")
	}
	zonesApi, _ := dnsProvider.Zones()
	zones, err := zonesApi.List()
	if err != nil {
		return fmt.Errorf("failed to list dns zones: %s", err)
	}
	zoneName := fmt.Sprintf("%s.", domainName)
	for _, zone := range zones {
		if zone.Name() == zoneName {
			rrName := fmt.Sprintf("%s.%s", name, zoneName)
			log.Infof("found zone %s, looking for %s record",
				zoneName, rrName)
			rrSets, _ := zone.ResourceRecordSets()
			rrSetList, err := rrSets.Get(rrName)
			if err != nil {
				return fmt.Errorf("failed to lookup record name %s: %s",
					rrName, err)
			}
			changeSet := rrSets.StartChangeset()
			var action string
			if delete {
				action = "deletion"
				if len(rrSetList) == 0 {
					log.Infof("not deleting DNS record because rrSetList empty")
					return nil
				} else{
					changeSet.Remove(rrSetList[0])
				}
			} else {
				rrSet := rrSets.New(rrName, []string{ip}, 180, rrstype.A)
				if len(rrSetList) == 0 {
					action = "creation"
				} else {
					action = "update"
					changeSet.Remove(rrSetList[0])
				}
				changeSet.Add(rrSet)
			}
			err = changeSet.Apply()
			if err != nil {
				return fmt.Errorf("%s of record set %s failed: %s",
					action, rrName, err)
			}
			log.Infof("%s of record set %s with ip %s succeeded",
				action, rrName, ip)
			return nil
		}
	}
	return fmt.Errorf("failed to find DNS zone %s", zoneName)
}

