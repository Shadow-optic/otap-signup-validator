package audit

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/otap/flr/internal/models"
)

func TestFrequencyFromWavelength(t *testing.T) {
	tests := []struct {
		name     string
		lambdaNm float64
		wantTHz  float64
	}{
		{"1550.00 nm", 1550.00, SpeedOfLightNm / (1550.00 * 1e12)},
		{"1530.33 nm (C-band start)", 1530.33, SpeedOfLightNm / (1530.33 * 1e12)},
		{"1565.50 nm (C-band end)", 1565.50, SpeedOfLightNm / (1565.50 * 1e12)},
		{"1310.00 nm", 1310.00, SpeedOfLightNm / (1310.00 * 1e12)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FrequencyFromWavelength(tt.lambdaNm)
			assert.InDelta(t, tt.wantTHz, got, 0.0001, "Frequency mismatch")
		})
	}
}

func TestWavelengthFromFrequency(t *testing.T) {
	tests := []struct {
		name     string
		freqTHz  float64
		wantNm   float64
	}{
		{"193.1 THz (ref)", 193.1, WavelengthFromFrequency(193.1)},
		{"192.1 THz", 192.1, SpeedOfLightNm / (192.1 * 1e12)},
		{"196.1 THz", 196.1, SpeedOfLightNm / (196.1 * 1e12)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WavelengthFromFrequency(tt.freqTHz)
			assert.InDelta(t, tt.wantNm, got, 0.001, "Wavelength mismatch")
		})
	}
}

func TestRoundTripConversion(t *testing.T) {
	// Converting wavelength -> frequency -> wavelength should return original
	originalWavelengths := []float64{1500.0, 1530.33, 1550.12, 1565.50, 1600.0}
	for _, lambda := range originalWavelengths {
		freq := FrequencyFromWavelength(lambda)
		got := WavelengthFromFrequency(freq)
		assert.InDelta(t, lambda, got, 0.0001, "Round-trip failed for %.2f nm", lambda)
	}
}

func TestIsOnGrid(t *testing.T) {
	tests := []struct {
		name       string
		lambdaNm   float64
		gridGHz    float64
		referenceNm float64
		want       bool
	}{
		{"50GHz grid - exact match at 1550.12", 1550.12, 50.0, 1550.12, true},
		{"50GHz grid - 0.4nm offset (~50GHz)", 1549.72, 50.0, 1550.12, true},
		{"25GHz grid - exact match", 1550.12, 25.0, 1550.12, true},
		{"100GHz grid - exact match", 1550.12, 100.0, 1550.12, true},
		{"50GHz grid - off grid", 1550.50, 50.0, 1550.12, false},
		{"50GHz grid - 0.2nm off", 1549.92, 50.0, 1550.12, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsOnGrid(tt.lambdaNm, tt.gridGHz, tt.referenceNm)
			assert.Equal(t, tt.want, got, "IsOnGrid(%.2f, %.1f, %.2f)", tt.lambdaNm, tt.gridGHz, tt.referenceNm)
		})
	}
}

func TestChannelNumberFromWavelength(t *testing.T) {
	tests := []struct {
		name     string
		lambdaNm float64
		gridGHz  float64
		wantCh   int32
	}{
		{"ref wavelength on 50GHz", 1552.524, 50.0, 0},
		{"ch +4 on 50GHz", 1550.92, 50.0, 4},
		{"ch -4 on 50GHz", 1554.13, 50.0, -4},
		{"ch +1 on 100GHz", 1552.12, 100.0, 1},
		{"ch -1 on 100GHz", 1552.93, 100.0, -1},
		{"ch 0 on 25GHz", 1552.524, 25.0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChannelNumberFromWavelength(tt.lambdaNm, tt.gridGHz)
			assert.Equal(t, tt.wantCh, got, "Channel number mismatch for %.3f nm on %.1f GHz grid", tt.lambdaNm, tt.gridGHz)
		})
	}
}

func TestWavelengthFromChannelNumber(t *testing.T) {
	tests := []struct {
		name    string
		chNum   int32
		gridGHz float64
	}{
		{"ch 0 on 50GHz", 0, 50.0},
		{"ch +1 on 50GHz", 1, 50.0},
		{"ch -1 on 50GHz", -1, 50.0},
		{"ch 0 on 25GHz", 0, 25.0},
		{"ch 10 on 100GHz", 10, 100.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WavelengthFromChannelNumber(tt.chNum, tt.gridGHz)
			// Round-trip check
			backCh := ChannelNumberFromWavelength(got, tt.gridGHz)
			assert.Equal(t, tt.chNum, backCh, "Round-trip channel number failed: got=%.4f nm, back=%d", got, backCh)
		})
	}
}

func TestValidateBand(t *testing.T) {
	tests := []struct {
		name     string
		lambdaNm float64
		band     models.Band
		wantErr  bool
	}{
		{"C-band valid - 1530.33", 1530.33, models.BandCBand, false},
		{"C-band valid - 1550.12", 1550.12, models.BandCBand, false},
		{"C-band valid - 1564.68", 1564.68, models.BandCBand, false},
		{"C-band invalid - 1529", 1529.0, models.BandCBand, true},
		{"C-band invalid - 1566", 1566.0, models.BandCBand, true},
		{"L-band valid - 1565.50", 1565.50, models.BandLBand, false},
		{"L-band valid - 1600.00", 1600.00, models.BandLBand, false},
		{"L-band valid - 1624.00", 1624.00, models.BandLBand, false},
		{"L-band invalid - 1564", 1564.0, models.BandLBand, true},
		{"L-band invalid - 1626", 1626.0, models.BandLBand, true},
		{"S-band valid - 1460.00", 1460.00, models.BandSBand, false},
		{"S-band valid - 1490.00", 1490.00, models.BandSBand, false},
		{"S-band valid - 1529.00", 1529.00, models.BandSBand, false},
		{"S-band invalid - 1459", 1459.0, models.BandSBand, true},
		{"S-band invalid - 1531", 1531.0, models.BandSBand, true},
		{"unspecified band", 1550.0, models.BandUnspecified, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateBand(tt.lambdaNm, tt.band)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGridSpacings(t *testing.T) {
	got := GridSpacings()
	assert.Equal(t, []float64{12.5, 25.0, 50.0, 100.0}, got)
}

func TestGridInfo(t *testing.T) {
	info := GridInfo()
	assert.Contains(t, info, "ITU-T G.694.1")
	assert.Contains(t, info, "C-band")
	assert.Contains(t, info, "L-band")
	assert.Contains(t, info, "S-band")
}

func TestSpeedOfLightConstant(t *testing.T) {
	// Verify speed of light constant is reasonable
	assert.InDelta(t, 2.998e17, SpeedOfLightNm, 0.001e17)
}

func TestKnownITUWavelengths(t *testing.T) {
	// These are known ITU-T wavelengths for 50 GHz grid around 193.1 THz
	// Channel 0 = 1552.524 nm
	ch0 := WavelengthFromChannelNumber(0, 50.0)
	assert.InDelta(t, ReferenceWavelengthNm, ch0, 0.001, "Channel 0 should be reference wavelength")

	// Frequency of channel 0 should be 193.1 THz
	freq0 := FrequencyFromWavelength(ch0)
	assert.InDelta(t, 193.1, freq0, 0.001, "Channel 0 frequency should be 193.1 THz")

	// Verify higher channel = shorter wavelength = higher frequency
	chPos1 := WavelengthFromChannelNumber(1, 50.0)
	chNeg1 := WavelengthFromChannelNumber(-1, 50.0)
	assert.True(t, chPos1 < ch0, "Channel +1 should have shorter wavelength than ch0")
	assert.True(t, chNeg1 > ch0, "Channel -1 should have longer wavelength than ch0")

	// Verify 50 GHz spacing: freq difference between ch+1 and ch0 should be ~0.05 THz
	freqCh1 := FrequencyFromWavelength(chPos1)
	diff := math.Abs(freqCh1 - freq0)
	assert.InDelta(t, 0.05, diff, 0.001, "50 GHz grid spacing should be 0.05 THz")

	// Verify 100 GHz spacing
	ch100_1 := WavelengthFromChannelNumber(1, 100.0)
	freq100_1 := FrequencyFromWavelength(ch100_1)
	diff100 := math.Abs(freq100_1 - freq0)
	assert.InDelta(t, 0.1, diff100, 0.001, "100 GHz grid spacing should be 0.1 THz")
}
