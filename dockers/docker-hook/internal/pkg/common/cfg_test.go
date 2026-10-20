package common

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"testing"
)

// operatorConf mirrors the conf.yaml the m5g-operator renders from a
// Mosaic5g spec (keys sorted, operator-only keys such as size included).
const operatorConf = `cnImage: mosaic5gecosys/oaicn:1.1
configurationPathofCN: /var/snap/oai-cn/current/
configurationPathofRAN: /var/snap/oai-ran/current/
dns: 192.168.106.12
downlinkFrequency: 2685000000L
eutraBand: "7"
flexRAN: true
flexRANDomainName: flexran
hssDomainName: oaicn
mcc: "208"
mmeDomainName: oaicn
mnc: "95"
mysqlDomainName: mysql
mysqlImage: mysql:5.6
ranImage: mosaic5gecosys/oairan:1.1
size: 1
snapBinaryPath: /snap/bin/
spgwDomainName: oaicn
uplinkFrequencyOffset: "-120000000"
`

func TestGetConfReadsOperatorConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf.yaml")
	if err := os.WriteFile(path, []byte(operatorConf), 0o600); err != nil {
		t.Fatal(err)
	}

	c := &Cfg{}
	if err := c.GetConf(log.New(io.Discard, "", 0), path); err != nil {
		t.Fatalf("GetConf: %v", err)
	}

	want := Cfg{
		MCC:                    "208",
		MNC:                    "95",
		EutraBand:              "7",
		DownlinkFrequency:      "2685000000L",
		UplinkFrequencyOffset:  "-120000000",
		FlexRAN:                true,
		ConfigurationPathofCN:  "/var/snap/oai-cn/current/",
		ConfigurationPathofRAN: "/var/snap/oai-ran/current/",
		SnapBinaryPath:         "/snap/bin/",
		DNS:                    "192.168.106.12",
		HssDomainName:          "oaicn",
		MmeDomainName:          "oaicn",
		SpgwDomainName:         "oaicn",
		MysqlDomainName:        "mysql",
		FlexRANDomainName:      "flexran",
	}
	if *c != want {
		t.Errorf("GetConf mismatch\n got: %+v\nwant: %+v", *c, want)
	}
}
