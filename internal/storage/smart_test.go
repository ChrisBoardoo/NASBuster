// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"os"
	"testing"
)

func TestParseSmartctlHealthy(t *testing.T) {
	r, retry := ParseSmartctl([]byte(`{"smartctl":{"exit_status":0},"device":{"type":"sat"},
		"smart_status":{"passed":true},"temperature":{"current":34},"power_on_time":{"hours":12345},
		"ata_smart_attributes":{"table":[
			{"id":1,"name":"Raw_Read_Error_Rate","raw":{"value":205734512}},
			{"id":5,"name":"Reallocated_Sector_Ct","raw":{"value":0}},
			{"id":199,"name":"UDMA_CRC_Error_Count","raw":{"value":3}}]}}`))
	if retry || !r.Available || r.Passed == nil || !*r.Passed || *r.TemperatureCelsius != 34 || *r.PowerOnHours != 12345 {
		t.Fatalf("report = %+v retry=%v", r, retry)
	}
	if len(r.Attributes) != 2 || r.Attributes[0].Status != "ok" || r.Attributes[1].Status != "warn" {
		t.Fatalf("attributes = %+v (attribute 1 must be filtered out)", r.Attributes)
	}
}

func TestParseSmartctlUnknownBridge(t *testing.T) {
	r, retry := ParseSmartctl([]byte(`{"smartctl":{"exit_status":1,"messages":[
		{"string":"/dev/sdb: Unknown USB bridge [0x152d:0x0578 (0x214)]","severity":"error"},
		{"string":"Please specify device type with the -d option.","severity":"error"}]}}`))
	if !retry || r.Available || r.Reason != SmartUSBBridgeUnsupported {
		t.Fatalf("report = %+v retry=%v", r, retry)
	}
}

func TestParseSmartctlPlainSCSI(t *testing.T) {
	r, retry := ParseSmartctl([]byte(`{"smartctl":{"exit_status":4},"device":{"type":"scsi"}}`))
	if !retry || r.Available || r.Reason != SmartUSBBridgeUnsupported {
		t.Fatalf("report = %+v retry=%v", r, retry)
	}
}

func TestParseSmartctlNVMe(t *testing.T) {
	r, _ := ParseSmartctl([]byte(`{"smartctl":{"exit_status":0},"device":{"type":"nvme"},
		"smart_status":{"passed":true},"temperature":{"current":41},
		"nvme_smart_health_information_log":{"critical_warning":0,"available_spare":100,"percentage_used":2,"media_errors":0}}`))
	if !r.Available || r.NVMe == nil || r.NVMe.PercentageUsed != 2 || r.NVMe.AvailableSpare != 100 {
		t.Fatalf("report = %+v", r)
	}
}

func TestParseSmartctlGarbage(t *testing.T) {
	r, retry := ParseSmartctl([]byte("not json"))
	if retry || r.Available || r.Reason != SmartError || r.Attributes == nil {
		t.Fatalf("report = %+v", r)
	}
}

// TestParseRealPi5NVMe uses smartctl 7.4's output for the AirDisk NVMe drive on
// the Pi 5 test unit (serial anonymized). smartctl exits with 4 because this
// DRAM-less drive rejects the self-test log page: the health data is still
// there and must be shown.
func TestParseRealPi5NVMe(t *testing.T) {
	data, err := os.ReadFile("testdata/pi5-nvme-smartctl.json")
	if err != nil {
		t.Fatal(err)
	}
	r, retry := ParseSmartctl(data)
	if !r.Available || retry || r.DeviceType != "nvme" {
		t.Fatalf("report = %+v, retry=%v", r, retry)
	}
	if r.Passed == nil || !*r.Passed || r.TemperatureCelsius == nil || *r.TemperatureCelsius != 24 {
		t.Errorf("passed/temperature = %v/%v", r.Passed, r.TemperatureCelsius)
	}
	if r.PowerOnHours == nil || *r.PowerOnHours != 127 {
		t.Errorf("power-on hours = %v", r.PowerOnHours)
	}
	if r.NVMe == nil || r.NVMe.PercentageUsed != 0 || r.NVMe.MediaErrors != 0 {
		t.Errorf("nvme health = %+v", r.NVMe)
	}
}
