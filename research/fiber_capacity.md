# Research Brief: Fiber Capacity Limits and Current State of the Art

**Date:** 2025  
**Topic:** Optical Fiber Shannon Limit, Commercial System Performance, SDM, and Headroom Analysis

---

## 1. The Nonlinear Shannon Limit

### 1.1 Fundamental Capacity Limit of Single-Mode Fiber (SMF-28) at C-Band

The C-band (1530-1565 nm) provides approximately **4.8 THz** of usable optical spectrum [^33^]. Under the linear Shannon limit (assuming only ASE noise), the maximum spectral efficiency for a dual-polarization system is:

```
SE_max = 2 x log2(1 + SNR)   [b/s/Hz]
```

However, the **nonlinear Shannon limit** -- the true practical constraint -- is governed by the Kerr effect (n2 = 2.7 x 10^-20 m^2/W), which creates signal-dependent nonlinear interference (NLI) from self-phase modulation (SPM), cross-phase modulation (XPM), and four-wave mixing (FWM) [^33^].

**Key capacity figures for standard SMF-28:**

| Configuration | Approx. Total BW | Practical Capacity Limit | Key Reference |
|---|---|---|---|
| C-band only | ~4.8 THz | ~40 Tb/s (transoceanic, 6,000-10,000 km) | [^33^] |
| C-band only | ~4.8 THz | ~50+ Tb/s (terrestrial, 1,000-2,000 km) | [^33^] |
| C+L band | ~9.6 THz | 71.6 Tb/s over 6,960 km (lab demo) | [^33^] |
| Super C+L | ~12 THz | 83.6 Tb/s over 1,240 km; >100 Tb/s projected | [^33^] |
| O+E+S+C+L+U bands | ~37.6 THz | 402 Tb/s over 50 km (NICT OFC 2024) | [^96^] |
| All bands (theoretical) | ~66 THz | Potentially >600 Tb/s in SMF (projected) | [^72^] |

The generally cited **fundamental practical limit for single-mode fiber using all available bands is approximately 100-400 Tb/s**, depending on distance and technology. For the C-band alone, experts estimate a practical ceiling of roughly **40-50 Tb/s** for long-haul links [^2^][^33^].

### 1.2 The EGN (Enhanced Gaussian Noise) Model

The Enhanced Gaussian Noise (EGN) model is an extension of the Gaussian Noise (GN) model that corrects for the non-Gaussian statistics of real modulation formats [^3^]. The standard GN model assumes the WDM signal is a Gaussian random process -- an assumption that becomes inaccurate for low-dispersion links or small channel counts. The EGN model adds correction terms that account for:

- **Modulation-format dependence** (kurtosis of the constellation)
- **Single-channel interference (SCI)** corrections
- **Cross-channel interference (XCI)** corrections
- **Multi-channel interference (MCI)** corrections

The EGN model delivers 5-15% better reach predictions than the GN model, particularly for systems with low accumulated dispersion [^5^]. It is the most widely used analytical framework for estimating the nonlinear Shannon limit in optical network planning tools (e.g., GnPy).

**EGN Model Prediction for a 1000 km Link:**

For a standard terrestrial link with 100 km spans, EDFA amplification (NF ~5 dB), and Nyquist-spaced WDM channels, the EGN model predicts:

| Distance | Predicted Max SE (per pol.) | Predicted Max SE (DP) | Reference |
|---|---|---|---|
| 1,000 km | ~3.5 b/s/Hz | ~7 b/s/Hz | [^56^] |
| 2,000 km | ~2.1-2.8 b/s/Hz | ~4.2-5.6 b/s/Hz | [^56^] |

These predictions assume uniform QAM constellations. With probabilistic constellation shaping (PCS), systems can operate within ~0.1-1 dB of the AWGN Shannon limit at practical SEs [^33^].

### 1.3 Nonlinear Shannon Limit in b/s/Hz for a 1000 km Link

For a typical 1000 km terrestrial link with standard EDFA amplification:

- **~7 b/s/Hz** (dual-polarization) is the approximate lower-bound spectral efficiency limit predicted by analytical models [^56^]
- In practice, deployed systems at 1000 km achieve **5-7 b/s/Hz** using DP-16QAM with PCS
- The best lab demonstrations have achieved **~6.3-7.3 b/s/Hz** at submarine-scale distances over C+L bands [^33^]
- At the optimum launch power, the GN model predicts the maximum effective OSNR is **2/3 of the linear OSNR** (a 1.76 dB penalty) -- this is the fundamental nonlinear penalty that cannot be overcome without changing the fiber or amplification architecture [^33^]

### 1.4 Gap-to-Capacity (GtC) of Current Commercial Systems

Modern coherent systems with PCS operate within **1-2 dB of the linear Shannon limit**. The remaining gap comes from:

| System Type | Spectral Efficiency | Gap to Linear Shannon | Gap to Nonlinear Shannon | Notes |
|---|---|---|---|---|
| 400G ZR (OIF) | ~4-6 b/s/Hz | ~1.5-2 dB | Moderate | 60-70 GBaud, DP-QPSK/8QAM [^1^] |
| 800G ZR (OIF) | ~8 b/s/Hz | ~1-1.5 dB | Significant | 120 GBaud, DP-16QAM, ~80-85% of Shannon [^33^][^11^] |
| 800G ZR+ | ~6-8 b/s/Hz | ~1-2 dB | Moderate | Adaptive PCS, longer reach [^11^] |
| Ciena WL6e 1.6T | ~10-12 b/s/Hz (metro) | ~1 dB | Metro-limited | 200 GBaud, 3nm DSP [^32^] |
| Nokia PSE-6s 1.2T | ~8 b/s/Hz | ~1 dB | Metro/Regional | 130 GBaud, 5nm DSP [^73^] |

**Submarine systems** operate within **30-50% of the nonlinear Shannon limit** in terms of spectral efficiency headroom -- the largest remaining gap [^33^].

**Key GtC insight:** The gap between the best commercial systems and the fundamental nonlinear Shannon limit is approximately **2-3 dB** for terrestrial links and **3-5 dB** for submarine links. Probabilistic constellation shaping provides up to **1.53 dB** theoretical shaping gain; deployed systems achieve **0.8-1.3 dB** of that gain [^33^].

---

## 2. Current Commercial Systems

### 2.1 Highest-Capacity Commercial Transponders

| Product | Vendor | Max Rate | Spectral Efficiency | Key Specs | Reference |
|---|---|---|---|---|---|
| WaveLogic 6 Extreme | Ciena | **1.6 Tb/s** | ~10-12 b/s/Hz (metro) | 200 GBaud, 3nm DSP, C+L band | [^32^][^35^] |
| PSE-6s | Nokia | **1.2 Tb/s** | ~8 b/s/Hz | 130 GBaud, 5nm DSP, 150 GHz | [^73^][^75^] |
| ICE6 | Infinera (Nokia) | 800 Gb/s | ~8 b/s/Hz | 140 GBaud, Nyquist subcarriers | [^57^] |
| Orion | Marvell (merchant) | 800 Gb/s | ~8 b/s/Hz | 130 GBaud, 5nm, QSFP-DD/OSFP | [^1^] |

**800G ZR Specifications (OIF Standard, October 2024):**
- Modulation: DP-16QAM
- Baud rate: ~120 GBaud
- Channel spacing: 150 GHz
- Spectral efficiency: ~8 b/s/Hz
- Reach: 80-120 km (ZR), extended with ZR+ modes
- Form factors: QSFP-DD, OSFP
- All 800G ZR modules support both 800ZR and ZR+ modes on day one [^11^]

### 2.2 Record Lab Demonstration for Single-Fiber Capacity (SMF)

The current record for single-mode fiber capacity is:

**402 Tb/s over 50 km** -- NICT, OFC 2024 Post-Deadline Paper [^96^]
- Authors: Benjamin J. Puttnam et al. (NICT and international partners)
- Venue: OFC 2024 Post-Deadline Session, March 28, 2024, San Diego
- Method: O+E+S+C+L+U band transmission using 6 doped-fiber amplifier variants + Raman amplification
- 1,505 WDM channels across 37.6 THz (275 nm, 1281.2-1649.9 nm)
- Modulation: DP-QAM up to 256-QAM
- GMI-estimated data rate: 402 Tb/s
- This exceeded the previous record (301 Tb/s) by over 25%

The **newer record** (ECOC 2025, post-deadline):
**430.2 Tb/s over 10 km** -- NICT, using a novel spatial-division approach in standard G.654 fiber [^7^][^103^]
- Combined single-mode transmission (E/S/C/L bands) with 3-mode transmission in O-band
- Total bandwidth: 30.1 THz (nearly 20% less than the 402 Tb/s experiment)
- 209 spatial super-channels in O-band + 706 channels across E/S/C/L bands

### 2.3 Record for Spectral Efficiency

| Record | Value | Group | Venue | Reference |
|---|---|---|---|---|
| Highest SE in SMF | ~10.7 b/s/Hz (average) | NICT (402 Tb/s / 37.6 THz) | OFC 2024 | [^96^] |
| Highest SE with SD-FEC | **6.21 b/s/Hz** (real-time transatlantic) | SubCom / Mertz | OFC 2019 | [^13^] |
| Record SE (spatial fiber) | **1935.6 b/s/Hz** | Sun Yat-sen U. (19-ring-core) | arXiv 2025 | [^14^] |
| SE with PCS (submarine) | 7.3 b/s/Hz over 6,600 km | NICT | Multiple | [^33^] |

The **1935.6 b/s/Hz** record was achieved using a 19-ring-core fiber supporting 266 OAM modes in the C+L bands over 10 km, with a GMI-estimated capacity of 25.24 Pb/s [^14^].

### 2.4 Modulation Formats at 800G

| Application | Primary Format | Baud Rate | SE (b/s/Hz) | Notes |
|---|---|---|---|---|
| 800G ZR (DCI) | **DP-16QAM** | ~120 GBaud | ~8 | OIF standard, 150 GHz spacing [^11^] |
| 800G ZR+ (metro) | **PCS-64QAM** or DP-16QAM | ~120 GBaud | 6-8 | OpenROADM MSA 6.0 PCS spec [^11^] |
| 800G long-haul | DP-8QAM / PCS-16QAM | ~90-120 GBaud | 4-6 | Adaptive modulation |
| 1.6T metro | PCS-64QAM / 256QAM | ~200 GBaud | 10-12 | Ciena WL6e only [^32^] |

**Probabilistic Constellation Shaping (PCS):** At 800G, PCS is critical for approaching the Shannon limit. PCS-64QAM achieves better than 1 dB gain over uniform 64QAM and comes within ~0.1 dB of the AWGN Shannon limit at 8 b/s/Hz [^33^]. Open PCS interoperability is specified in the OpenROADM MSA 6.0 for 800G ZR+ applications [^11^].

---

## 3. Multiplexing Dimensions Already Used

### 3.1 WDM Channels in C+L+S Bands

| Band | Wavelength Range | Bandwidth | WDM Channels (50 GHz) | Amplification |
|---|---|---|---|---|
| O-band | 1260-1360 nm | ~17 THz | Research only | PDFA (fluoride) |
| E-band | 1360-1460 nm | ~7 THz | Research only | BDFA |
| S-band | 1460-1530 nm | ~4 THz | Research/limited | TDFA / Raman |
| **C-band** | **1530-1565 nm** | **~4.8 THz** | **~80-160** | **EDFA (mature)** |
| **L-band** | **1565-1625 nm** | **~4.8 THz** | **~80-160** | **EDFA (mature)** |
| U-band | 1625-1675 nm | ~3 THz | Research only | Raman / specialty |
| **C+L (Super)** | **1524-1572 + 1572-1625** | **~12 THz** | **~240** | **Dual EDFA** |
| **O+E+S+C+L+U** | **1260-1675 nm** | **~37.6 THz** | **1,505** | **Multiple (lab)** |

**Commercial state:** C+L band systems are now mainstream in submarine cables. The NICT 402 Tb/s experiment demonstrated that all bands (OESCLU) can be used simultaneously with custom amplification, covering 37.6 THz [^96^]. Commercial submarine systems typically use C-band only or C+L.

### 3.2 Current State of SDM (Space Division Multiplexing)

**Commercial SDM (Submarine Cables):**
SDM in commercial submarine cables uses **parallel single-mode fiber pairs** (not multi-core fiber). Current systems:

| Cable System | Fiber Pairs | Capacity | Year | Reference |
|---|---|---|---|---|
| Dunant (Google) | 12 | 250 Tb/s | 2021 | [^57^] |
| H2HE (China Mobile) | **16** | 307 Tb/s | 2021 | [^57^][^58^] |
| Grace Hopper (Google) | 16 | 352 Tb/s | 2022 | [^57^] |
| Amitie | 16 | 320 Tb/s | 2022 | [^57^] |
| Confluence-1 | **24** | >500 Tb/s | 2023 | [^57^] |
| Medusa | **24** | 480 Tb/s | 2024 | [^57^] |
| I-AM Cable | 16 | ~320 Tb/s | 2026+ | [^59^] |

Commercial SDM cables now routinely use **12-24 fiber pairs**, achieving total cable capacities of **250-500+ Tb/s** [^57^]. Multi-core fiber has **not** yet been deployed commercially in submarine systems [^60^].

**Research SDM (Multi-Core Fiber - MCF):**

| Fiber Type | Cores/Modes | Capacity | Distance | Year | Reference |
|---|---|---|---|---|---|
| 19-core (randomly coupled) | 19 cores | **1.7 Pb/s** | 63.5 km | 2023 | [^37^][^44^] |
| 19-core (low-loss) | 19 cores | **1.02 Pb/s** | **1,808 km** | 2025 | [^39^][^40^] |
| 15-mode fiber | 15 modes | 0.273 Pb/s | 1,001 km | Prior | [^40^] |
| 19-ring-core fiber | 266 OAM modes | **25.24 Pb/s** (GMI) | 10 km | 2025 | [^14^] |
| 12-core fiber | 12 cores | 0.52 Pb/s | 8,830 km | Prior | [^33^] |

**Few-Mode Fiber (FMF) Demonstrations:**
NIST and other groups have demonstrated 15-mode fiber transmission achieving 0.273 Pb/s over 1,001 km [^40^]. The challenge with FMF is the large differences in propagation characteristics between modes, which break their orthogonality over distance. Randomly coupled multi-core fibers overcome this by having cores with matched propagation characteristics [^44^].

### 3.3 Spatial Modes Demonstrated

The highest spatial mode count demonstrated to date:
- **19-core fiber x 2 polarizations x ~1 mode per core = 38 spatial channels** (in 19-core MCF) [^39^]
- **19-ring-core fiber supporting 266 OAM modes** (record) [^14^]
- **266 OAM modes x C+L bands** produced the 25.24 Pb/s capacity record [^14^]

For practical long-haul transmission, the 19-core randomly coupled fiber with standard 0.125 mm cladding diameter is the most promising near-term SDM technology, having achieved 1.02 Pb/s over 1,808 km [^39^].

### 3.4 Crosstalk Penalties Between Spatial Modes

| Crosstalk Type | Typical Value | Impact | Reference |
|---|---|---|---|
| Inter-core crosstalk (uncoupled MCF) | -30 to -55 dB/100km | Acceptable for long-haul | [^101^] |
| Inter-core crosstalk (coupled MCF) | Managed via MIMO DSP | Requires 19x19 MIMO | [^44^] |
| Crosstalk penalty (7-core MCF) | ~1.5 dB at BER 10^-9 | Moderate penalty | [^100^] |
| Crosstalk penalty (9-core MCF) | ~2.5 dB at BER 10^-9 | Higher penalty | [^100^] |
| XT spec proposed for standardization | **-60 dB/km** | Universal spec for all systems | [^102^] |

A crosstalk level below **-30 dB per 100 km** is generally required for long-distance reliable signal transmission [^101^]. With trench-assisted designs, inter-core crosstalk can be reduced to below **-40 dB** as required for long-haul [^97^]. A universal specification of **-60 dB/km** has been proposed for all MCF systems [^102^].

---

## 4. Where is the Headroom?

### 4.1 When Do We Hit the Fundamental Limit?

| Metric | Current | Projected Limit | Headroom | Timeline |
|---|---|---|---|---|
| C-band only (terrestrial) | ~25-35 Tb/s deployed | ~50 Tb/s | ~2x | Near-term |
| C-band only (submarine) | ~20-25 Tb/s/FP | ~35-40 Tb/s | ~1.5-2x | 3-5 years |
| C+L band (submarine) | ~35-50 Tb/s/FP | ~71-100 Tb/s | ~2x | 5-10 years |
| All bands (lab only) | 402 Tb/s over 50 km | ~600+ Tb/s | ~1.5x | Research |
| Multi-core fiber (lab) | 1.02 Pb/s over 1,808 km | ~10+ Pb/s (theory) | ~10x+ | Research |

**For single-mode fiber with conventional amplification (C-band only):** The industry is already within **2-3x** of the practical fundamental limit for terrestrial links. Submarine links are within **1.5-2x** of the C-band nonlinear limit [^33^].

**Traffic growth** is projected at **30-40% annually** [^69^]. At this rate, networks may need **100x current capacity** within a dozen years [^69^]. The capacity of single-mode fiber using all bands is estimated at roughly **100 Tbps** practically, with lab demonstrations already exceeding 400 Tb/s over short distances [^2^][^96^].

### 4.2 Approaches Being Pursued to Push Past Current Limits

| Approach | Potential Gain | Status | Key Reference |
|---|---|---|---|
| **C+L band expansion** | 2x capacity | Commercially deployed | [^33^] |
| **Super C+L (12 THz)** | 2.5x vs C-band | Commercially available | [^71^] |
| **Multi-band (OESCLU)** | 7-8x vs C-band | Lab demonstration only | [^96^] |
| **SDM (more fiber pairs)** | 2-3x capacity | Commercial (submarine) | [^57^] |
| **Multi-core fiber** | 10-100x capacity | Lab (up to 19 cores) | [^39^] |
| **Few-mode fiber + MIMO** | 10-50x capacity | Lab (up to 266 modes) | [^14^] |
| **Digital Backpropagation (DBP)** | 1-2 dB SNR improvement | Limited commercial use | [^33^] |
| **Raman amplification** | 3-6 dB OSNR improvement | Commercial in submarine | [^66^] |
| **Hollow-core fiber** | 2-3x capacity, lower latency | Pre-commercial trials | [^66^][^72^] |
| **Lower-loss fiber (0.14 dB/km)** | 1-2 dB improvement | Commercial (PSCF) | [^68^] |
| **Probabilistic Constellation Shaping** | 0.8-1.5 dB gain | Commercially standard | [^33^] |
| **Advanced FEC (staircase/SPFEC)** | 0.3-0.5 dB gain | Commercial (submarine) | [^33^] |

### 4.3 Hollow-Core Fiber: The Most Disruptive Near-Term Opportunity

Hollow-core fiber (HCF) offers three transformative advantages [^66^][^72^]:

1. **Ultra-low nonlinearity:** Nonlinear coefficient is **3-4 orders of magnitude lower** than SMF (5.01 x 10^-4 vs 1.12 1/(W.km)) [^68^]
2. **Ultra-low latency:** ~33% lower latency than SMF (light travels ~50% faster in air/vacuum) [^68^]
3. **Lower loss:** Record HCF has achieved **0.05-0.09 dB/km** at 1550 nm, surpassing SMF's ~0.14 dB/km fundamental limit [^72^][^74^]
4. **No Raman scattering:** Eliminates inter-channel SRS power transfer, enabling ultra-wideband DWDM [^66^]

**Impact:** HCF can support 1.2T DP-64QAM with 3x higher spectral efficiency than SMF for the same distance. Long-distance 1.6T PS-64QAM transmission can extend reach by **~10x** compared to SMF [^66^]. Microsoft has announced plans to deploy **15,000 km of hollow-core fiber** within 24 months [^67^].

### 4.4 Quantification of Remaining Headroom

| Dimension | Current Commercial Best | Practical Limit | Headroom (factor) |
|---|---|---|---|
| **C-band SMF (terrestrial 1000 km)** | ~25-35 Tb/s | ~50 Tb/s | **~1.5-2x** |
| **C-band SMF (submarine 10,000 km)** | ~20-25 Tb/s/FP | ~35-40 Tb/s | **~1.5-2x** |
| **C+L SMF** | ~50-71 Tb/s | ~100 Tb/s | **~1.5-2x** |
| **All bands SMF (short reach)** | 402 Tb/s (lab) | ~600-800 Tb/s (est.) | **~1.5-2x** |
| **Multi-core fiber (19-core)** | 1.02 Pb/s/1808 km (lab) | ~10+ Pb/s (theory) | **~10x+** |
| **Commercial submarine cable (SDM)** | 352-500 Tb/s (24 FP) | ~1 Pb/s (projected) | **~2-3x** |

### 4.5 Summary Assessment

**Single-mode fiber is approaching its practical capacity ceiling for long-haul C-band transmission.** The industry has approximately **1.5-2x headroom** remaining in the C-band for submarine links, and roughly **2-3x** for terrestrial links before fundamental nonlinear limits are reached.

**The primary near-to-medium term scaling vectors are:**

1. **Spectrum expansion** (C+L, and eventually S+L+U bands) -- provides 2-3x linear capacity scaling
2. **SDM with more fiber pairs** (12->24->32 fiber pairs) -- already commercial, provides 2-3x
3. **Higher baud rates** (120->200->300+ GBaud) -- gradual 1.5-2x improvement
4. **Hollow-core fiber** -- potentially 2-3x capacity increase plus latency reduction, pre-commercial
5. **Multi-core fiber** -- 10x+ in lab but requires new infrastructure and MIMO DSP, long-term

**Combined, these approaches could extend single-fiber-pair capacity to 100+ Tb/s commercially and cable capacity to 1+ Pb/s** using multi-core or multi-pair SDM architectures. However, C-band single-mode fiber alone has at most **2-3x** headroom remaining for long-haul applications.

---

## Key References

1. OFC 2024 Post-Deadline: B.J. Puttnam et al., "402 Tb/s GMI data-rate OESCLU-band Transmission," OFC 2024.
2. ECOC 2025 Post-Deadline: NICT, "430 Tb/s in standard G.654 fiber using few- and single-mode transmission," ECOC 2025.
3. NICT Press Release (2025): "World Record 1,808 km Transmission of 1.02 Petabits per Second with 19-core Optical Fiber," OFC 2025 Post-Deadline.
4. arXiv:2506.04910: H. Li et al., "Record-Breaking 1935.6 bit/s/Hz Spectral Efficiency in 19-Ring-Core Fiber," 2025.
5. OFC 2023 Post-Deadline: NICT/SEI, "Randomly Coupled 19-Core Multi-Core Fiber with Standard Cladding Diameter."
6. MapYourTech, "Shannon's Limits for Fiber Optics," 2025.
7. Ciena WaveLogic 6 Extreme datasheets and press releases, 2023-2024.
8. Nokia PSE-6s announcements and field trial results, 2023-2024.
9. OIF 800ZR Implementation Agreement, October 2024.
10. Nature Photonics: DNANF hollow-core fiber with <0.1 dB/km loss, 2025.
11. MDPI: "Toward SDM-Based Submarine Optical Networks," 2022.
12. MapYourTech: "The Gaussian Noise Model in Optical Networking," 2026.

---

*Compiled from publicly available technical papers, conference proceedings (OFC, ECOC), press releases, and industry analyses.*
