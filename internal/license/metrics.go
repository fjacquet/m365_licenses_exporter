package license

// SeatSample builds a seats_total/seats_used sample with the canonical
// {instance,product,unit,vendor} label set (sorted by key).
func SeatSample(name, vendor, product, unit, instance string, v float64) Sample {
	return Sample{
		Name: name,
		Labels: []Label{
			{Key: "instance", Value: instance},
			{Key: "product", Value: product},
			{Key: "unit", Value: unit},
			{Key: "vendor", Value: vendor},
		},
		Value: v,
	}
}

// ExpirationSample builds a license_expiration_timestamp_seconds sample
// ({instance,product,vendor}). Callers omit it entirely for perpetual licenses.
func ExpirationSample(vendor, product, instance string, tsUnix float64) Sample {
	return Sample{
		Name: MetricExpiration,
		Labels: []Label{
			{Key: "instance", Value: instance},
			{Key: "product", Value: product},
			{Key: "vendor", Value: vendor},
		},
		Value: tsUnix,
	}
}

func vendorInstanceLabels(vendor, instance string) []Label {
	return []Label{
		{Key: "instance", Value: instance},
		{Key: "vendor", Value: vendor},
	}
}

// UpSample builds license_up{vendor,instance}.
func UpSample(vendor, instance string, up bool) Sample {
	v := 0.0
	if up {
		v = 1.0
	}
	return Sample{Name: MetricUp, Labels: vendorInstanceLabels(vendor, instance), Value: v}
}

// LastSuccessSample builds license_collector_last_success_timestamp_seconds.
func LastSuccessSample(vendor, instance string, tsUnix float64) Sample {
	return Sample{Name: MetricLastSuccess, Labels: vendorInstanceLabels(vendor, instance), Value: tsUnix}
}

// ScrapeDurationSample builds license_scrape_duration_seconds.
func ScrapeDurationSample(vendor, instance string, seconds float64) Sample {
	return Sample{Name: MetricScrapeDuration, Labels: vendorInstanceLabels(vendor, instance), Value: seconds}
}

// BuildInfoSample builds the constant license_build_info gauge (value 1).
func BuildInfoSample(version, goVersion string) Sample {
	return Sample{
		Name: MetricBuildInfo,
		Labels: []Label{
			{Key: "goversion", Value: goVersion},
			{Key: "version", Value: version},
		},
		Value: 1,
	}
}
