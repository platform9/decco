package dns

import (
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"k8s.io/federation/pkg/dnsprovider"
	_ "k8s.io/federation/pkg/dnsprovider/providers/aws/route53"
	"k8s.io/federation/pkg/dnsprovider/rrstype"
)

var (
	dnsProvider dnsprovider.Interface
	log         = logrus.WithField("pkg", "dns")
)

func init() {
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

func DeleteRecord(domainName string, name string, ipOrHostname string,
	isHostname bool) error {
	return UpdateRecord(domainName, name, ipOrHostname, isHostname, true)
}

func CreateOrUpdateRecord(domainName string, name string, ipOrHostname string,
	isHostname bool) error {
	return UpdateRecord(domainName, name, ipOrHostname, isHostname, false)
}

func UpdateRecord(domainName string, name string, ipOrHostname string,
	isHostname bool, delete bool) error {
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
				} else {
					changeSet.Remove(rrSetList[0])
				}
			} else {
				rscType := rrstype.A
				if isHostname {
					rscType = rrstype.CNAME
				}
				rrSet := rrSets.New(rrName, []string{ipOrHostname}, 180, rscType)
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
			log.Infof("%s of record set %s with %s succeeded",
				action, rrName, ipOrHostname)
			return nil
		}
	}
	return fmt.Errorf("failed to find DNS zone %s", zoneName)
}
