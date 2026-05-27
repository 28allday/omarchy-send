package config

import (
	"crypto/rand"
	"math/big"
)

// Colour + celestial-object name parts, all copyright-safe. Colours are generic
// English; the objects are public-domain astronomical terms — types of stars
// and galaxies (Nova, Quasar, Pulsar, Spiral, …). No trademarked or franchise
// names are used, so a generated alias reads like a celestial body ("Crimson
// Nova") with no IP exposure.
//
// This exists because the alias is broadcast in plaintext over multicast to the
// whole subnet every few seconds while the TUI is open. On a roaming laptop the
// machine hostname would leak to strangers on untrusted networks; a randomised
// alias avoids that, matching LocalSend's behaviour. The hostname is still
// carried in DeviceModel and any alias is overridable via --alias or Settings.
var (
	aliasColours = []string{
		"Crimson", "Scarlet", "Azure", "Cobalt", "Emerald", "Amber",
		"Ivory", "Onyx", "Silver", "Graphite", "Burgundy", "Teal",
		"Indigo", "Bronze", "Pearl", "Slate", "Olive", "Maroon",
		"Turquoise", "Gold", "Copper", "Jade", "Ruby", "Sapphire",
		"Midnight", "Sand", "Charcoal", "Cream",
		"Coral", "Violet", "Lavender", "Plum", "Rose", "Salmon",
		"Peach", "Saffron", "Mustard", "Lime", "Mint", "Sage",
		"Forest", "Cyan", "Sky", "Navy", "Steel", "Pewter",
		"Ash", "Ebony", "Obsidian", "Cherry", "Rust", "Sienna",
		"Tan", "Khaki", "Brass", "Platinum",
	}
	aliasObjects = []string{
		"Nova", "Supernova", "Pulsar", "Quasar", "Magnetar", "Nebula",
		"Comet", "Meteor", "Aurora", "Corona", "Eclipse", "Cosmos",
		"Galaxy", "Halo", "Spiral", "Starburst", "Dwarf", "Giant",
		"Supergiant", "Hypergiant", "Cluster", "Drift",
		"Blazar", "Protostar", "Neutron", "Binary", "Cepheid", "Andromeda",
		"Whirlpool", "Pinwheel", "Sombrero", "Triangulum", "Sunflower", "Flare",
		"Plasma", "Photon", "Zenith", "Apogee", "Solstice", "Equinox",
		"Meridian", "Vortex", "Ember", "Beacon",
	}
)

// randomAlias returns a colour + celestial-object display name, e.g.
// "Crimson Nova". It is generated once on first run and persisted by Load, so a
// device keeps the same name across restarts.
func randomAlias() string {
	return pick(aliasColours) + " " + pick(aliasObjects)
}

// pick chooses a uniformly random element using crypto/rand (no seeding, always
// unpredictable). It falls back to the first element only if the RNG errors.
func pick(s []string) string {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(s))))
	if err != nil {
		return s[0]
	}
	return s[n.Int64()]
}
