package chat

// HeroRole is one of the fixed identities the server hands out. The list lives on
// the server only: the client renders whatever name and emoji it is given, so a
// browser can no longer pick (or spoof) an identity.
type HeroRole struct {
	Name  string `json:"name"`
	Emoji string `json:"emoji"`
}

var HERO_POOL = []HeroRole{
	{Name: "VOLT", Emoji: "⚡"},
	{Name: "NINJA", Emoji: "🗡️"},
	{Name: "BLAZE", Emoji: "🔥"},
	{Name: "FROST", Emoji: "❄️"},
	{Name: "TOXIC", Emoji: "🧪"},
	{Name: "MECH", Emoji: "💥"},
	{Name: "VOID", Emoji: "🔮"},
	{Name: "TITAN", Emoji: "🛡️"},
	{Name: "AERO", Emoji: "🌪️"},
	{Name: "PSYCHO", Emoji: "👁️"},
}
