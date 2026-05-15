// Package audit provides ITU-T compliance checking and audit functionality for the FLR system.
package audit

import (
	"fmt"
	"math"

	"github.com/otap/flr/internal/models"
)

// ITU-T Grid constants for optical fiber bands.
const (
	CBandStartNm = 1530.0
	CBandEndNm   = 1565.0
	LBandStartNm = 1565.0
	LBandEndNm   = 1625.0
	SBandStartNm = 1460.0
	SBandEndNm   = 1530.0

	// Speed of light in nm/s.
	SpeedOfLightNm = 2.99792458e17
	// Speed of light in m/s.
	SpeedOfLightMS = 2.99792458e8

	// ITU-T reference wavelength for channel numbering (nm).
	ReferenceWavelengthNm = 1552.524
	// ITU-T reference frequency (THz).
	ReferenceFrequencyTHz = 193.1
)

// FrequencyFromWavelength converts a wavelength in nanometers to frequency in THz.
func FrequencyFromWavelength(lambdaNm float64) float64 {
	return SpeedOfLightNm / (lambdaNm * 1e12)
}

// WavelengthFromFrequency converts a frequency in THz to wavelength in nanometers.
func WavelengthFromFrequency(freqTHz float64) float64 {
	return SpeedOfLightNm / (freqTHz * 1e12)
}

// IsOnGrid checks if a wavelength falls on the specified DWDM grid spacing.
func IsOnGrid(lambdaNm float64, gridGHz float64, referenceNm float64) bool {
	refFreq := FrequencyFromWavelength(referenceNm)
	freq := FrequencyFromWavelength(lambdaNm)
	diff := math.Abs(freq - refFreq)
	// Round to nearest grid slot
	slot := diff / (gridGHz / 1000.0)
	nearest := math.Round(slot) * (gridGHz / 1000.0)
	// Allow 0.0001 THz tolerance for floating point
	return math.Abs(diff-nearest) < 0.0001
}

// ChannelNumberFromWavelength calculates the ITU-T channel number from a wavelength.
func ChannelNumberFromWavelength(lambdaNm float64, gridGHz float64) int32 {
	refFreq := FrequencyFromWavelength(ReferenceWavelengthNm)
	freq := FrequencyFromWavelength(lambdaNm)
	diff := freq - refFreq // positive if shorter wavelength (higher freq)
	ratio := diff / (gridGHz / 1000.0)
	return int32(math.Round(ratio))
}

// WavelengthFromChannelNumber calculates the wavelength in nm from an ITU-T channel number.
func WavelengthFromChannelNumber(chNum int32, gridGHz float64) float64 {
	freq := ReferenceFrequencyTHz + (float64(chNum) * (gridGHz / 1000.0))
	return WavelengthFromFrequency(freq)
}

// ValidateBand checks if the given wavelength falls within the specified optical band range.
func ValidateBand(lambdaNm float64, band models.Band) error {
	switch band {
	case models.BandCBand:
		if lambdaNm < CBandStartNm || lambdaNm > CBandEndNm {
			return fmt.Errorf("wavelength %.2f nm not in C-band range [%.0f, %.0f]", lambdaNm, CBandStartNm, CBandEndNm)
		}
	case models.BandLBand:
		if lambdaNm < LBandStartNm || lambdaNm > LBandEndNm {
			return fmt.Errorf("wavelength %.2f nm not in L-band range [%.0f, %.0f]", lambdaNm, LBandStartNm, LBandEndNm)
		}
	case models.BandSBand:
		if lambdaNm < SBandStartNm || lambdaNm > SBandEndNm {
			return fmt.Errorf("wavelength %.2f nm not in S-band range [%.0f, %.0f]", lambdaNm, SBandStartNm, SBandEndNm)
		}
	default:
		return fmt.Errorf("unsupported band: %s", band.String())
	}
	return nil
}

// GridSpacings returns the supported DWDM grid spacings in GHz.
func GridSpacings() []float64 {
	return []float64{12.5, 25.0, 50.0, 100.0}
}

// GridInfo returns human-readable grid information.
func GridInfo() string {
	return "ITU-T G.694.1 DWDM grid: C-band (1530-1565nm), L-band (1565-1625nm), S-band (1460-1530nm)"
}
