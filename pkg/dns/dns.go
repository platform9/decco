package dns

import (
	"fmt"
	"net"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
	"k8s.io/federation/pkg/dnsprovider"
	_ "k8s.io/federation/pkg/dnsprovider/providers/aws/route53"
	"k8s.io/federation/pkg/dnsprovider/rrstype"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	ProviderRoute53 = "route53"
	ProviderFake    = "fake"
)

var (
	log = logrus.WithField("pkg", "dns")
)

type Provider interface {
	DeleteRecord(domainName string, name string, ipOrHostname string) error
	CreateOrUpdateRecord(domainName string, name string, ipOrHostname string) error
	UpdateRecord(domainName string, name string, ipOrHostname string, delete bool) error
}

// CreateProvider creates a provider for the given DNS service provider.
// Supported values for dnsProviderName: [fake, route53]
func CreateProviderFromName(dnsProviderName string) (Provider, error) {
	switch dnsProviderName {
	case ProviderFake:
		return NewFakeProvider(nil), nil
	case ProviderRoute53:
		externalDNSProvider, err := dnsprovider.GetDnsProvider(dnsProviderName, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get dns provider %s", dnsProviderName)
		}
		return &KubeFedDNSProvider{
			externalDNSProvider: externalDNSProvider,
		}, nil
	default:
		return nil, fmt.Errorf("unknown DNS provider name: %s", dnsProviderName)
	}
}

type FakeProvider struct {
	log logr.Logger
}

func NewFakeProvider(log logr.Logger) *FakeProvider {
	if log == nil {
		log = ctrl.Log
	}

	return &FakeProvider{
		log: log.WithName("dns.FakeProvider"),
	}
}

func (m *FakeProvider) DeleteRecord(domainName string, name string, ipOrHostname string) error {
	m.log.Info("[MOCK] Delete DNS record(s). ", "name", name, "domainName", domainName, "ipOrHostname", ipOrHostname)
	return nil
}

func (m *FakeProvider) CreateOrUpdateRecord(domainName string, name string, ipOrHostname string) error {
	m.log.Info("[MOCK] Create or update DNS record(s). ", "name", name, "domainName", domainName, "ipOrHostname", ipOrHostname)
	return nil
}

func (m *FakeProvider) UpdateRecord(domainName string, name string, ipOrHostname string, delete bool) error {
	m.log.Info("[MOCK] Update DNS record(s). ", "name", name, "domainName", domainName, "ipOrHostname", ipOrHostname, "delete", delete)
	return nil
}

type KubeFedDNSProvider struct {
	externalDNSProvider dnsprovider.Interface
}

func (p *KubeFedDNSProvider) DeleteRecord(domainName string, name string, ipOrHostname string) error {
	return p.UpdateRecord(domainName, name, ipOrHostname, true)
}

func (p *KubeFedDNSProvider) CreateOrUpdateRecord(domainName string, name string, ipOrHostname string) error {
	return p.UpdateRecord(domainName, name, ipOrHostname, false)
}

func (p *KubeFedDNSProvider) UpdateRecord(domainName string, name string, ipOrHostname string, delete bool) error {
	zonesApi, _ := p.externalDNSProvider.Zones()
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
				var rscType rrstype.RrsType
				addr := net.ParseIP(ipOrHostname)
				if addr == nil {
					rscType = rrstype.CNAME
				} else {
					rscType = rrstype.A
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
