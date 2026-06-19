package snippet

import (
	"strings"
)

var popularEmojis = []EmojiInfo{
	{"😂", "Face with Tears of Joy", []string{"laugh", "lol", "funny", "joy", "haha"}},
	{"❤️", "Red Heart", []string{"love", "like", "heart"}},
	{"👍", "Thumbs Up", []string{"yes", "ok", "good", "agree", "like", "approve"}},
	{"🔥", "Fire", []string{"hot", "cool", "lit", "awesome", "burn"}},
	{"😊", "Smiling Face with Smiling Eyes", []string{"happy", "smile", "friendly"}},
	{"🎉", "Partying Face", []string{"celebrate", "party", "congrats", "tada", "event"}},
	{"🤔", "Thinking Face", []string{"think", "question", "wonder", "hmm"}},
	{"😭", "Loudly Crying Face", []string{"cry", "sad", "sobbing", "tear"}},
	{"😔", "Pensive Face", []string{"sad", "sorry", "depressed", "pensiv", "pensive", "worry"}},
	{"🚀", "Rocket", []string{"fast", "launch", "ship", "space", "fly"}},
	{"👀", "Eyes", []string{"look", "see", "watch", "glance"}},
}

var emojiDatabase = []EmojiInfo{
	// Smileys & Emotion
	{"😀", "Grinning Face", []string{"smile", "happy", "grin"}},
	{"😃", "Grinning Face with Big Eyes", []string{"smile", "happy", "grin"}},
	{"😄", "Grinning Face with Smiling Eyes", []string{"smile", "happy", "grin"}},
	{"😁", "Beaming Face with Smiling Eyes", []string{"smile", "happy", "grin", "teeth"}},
	{"😆", "Grinning Squinting Face", []string{"smile", "laugh", "happy"}},
	{"😅", "Grinning Face with Sweat", []string{"smile", "happy", "nervous", "sweat"}},
	{"😂", "Face with Tears of Joy", []string{"laugh", "lol", "funny", "joy", "haha"}},
	{"🤣", "Rolling on the Floor Laughing", []string{"laugh", "lol", "funny", "rofl"}},
	{"😊", "Smiling Face with Smiling Eyes", []string{"happy", "smile", "friendly"}},
	{"😇", "Smiling Face with Halo", []string{"angel", "halo", "innocent"}},
	{"🙂", "Slightly Smiling Face", []string{"happy", "smile"}},
	{"🙃", "Upside-Down Face", []string{"sarcasm", "silly"}},
	{"😉", "Winking Face", []string{"wink", "tease"}},
	{"😌", "Relieved Face", []string{"relieved", "calm", "peace"}},
	{"😍", "Smiling Face with Heart-Eyes", []string{"love", "like", "heart", "adore"}},
	{"🥰", "Smiling Face with Hearts", []string{"love", "affection", "warm"}},
	{"😘", "Face Blowing a Kiss", []string{"love", "kiss"}},
	{"😋", "Face Savoring Food", []string{"yummy", "hungry", "delicious"}},
	{"😛", "Face with Tongue", []string{"playful", "tongue"}},
	{"😜", "Winking Face with Tongue", []string{"playful", "wink", "tongue"}},
	{"🤪", "Zany Face", []string{"crazy", "wild", "goofy"}},
	{"🤨", "Face with Raised Eyebrow", []string{"skeptical", "suspicious", "hm"}},
	{"🧐", "Face with Monocle", []string{"smart", "inspect", "monocle"}},
	{"🤓", "Nerd Face", []string{"nerd", "smart", "geek", "glasses"}},
	{"😎", "Smiling Face with Sunglasses", []string{"cool", "sunglasses", "chill"}},
	{"🥳", "Partying Face", []string{"party", "celebrate", "congrats"}},
	{"😏", "Smirking Face", []string{"smirk", "sly"}},
	{"😒", "Unamused Face", []string{"annoyed", "unhappy"}},
	{"😞", "Disappointed Face", []string{"sad", "disappointed"}},
	{"😔", "Pensive Face", []string{"sad", "sorry", "depressed", "pensiv", "pensive", "worry"}},
	{"😟", "Worried Face", []string{"worry", "nervous"}},
	{"😕", "Confused Face", []string{"confused", "huh"}},
	{"🙁", "Slightly Frowning Face", []string{"sad", "frown"}},
	{"☹️", "Frowning Face", []string{"sad", "frown"}},
	{"🥺", "Pleading Face", []string{"plead", "beg", "sad"}},
	{"😢", "Crying Face", []string{"cry", "sad", "tear"}},
	{"😭", "Loudly Crying Face", []string{"cry", "sad", "sobbing"}},
	{"😤", "Face with Steam from Nose", []string{"angry", "triumph", "huff"}},
	{"😠", "Angry Face", []string{"angry", "mad"}},
	{"😡", "Pouting Face", []string{"angry", "mad", "rage"}},
	{"🤬", "Face with Symbols on Mouth", []string{"swear", "angry", "curse"}},
	{"🤯", "Exploding Head", []string{"mindblown", "shock", "wow"}},
	{"😳", "Flushed Face", []string{"blush", "embarrassed", "shock"}},
	{"🥵", "Hot Face", []string{"hot", "heat", "fever"}},
	{"🥶", "Cold Face", []string{"cold", "freeze"}},
	{"😱", "Face Screaming in Fear", []string{"scared", "fear", "shock", "scream"}},
	{"🥱", "Yawning Face", []string{"tired", "sleepy", "yawn"}},
	{"😴", "Sleeping Face", []string{"sleep", "zzz"}},
	{"🤤", "Drooling Face", []string{"hungry", "drool"}},
	{"😪", "Sleepy Face", []string{"tired", "sleepy"}},
	{"😵", "Dizzy Face", []string{"dizzy", "dead"}},
	{"🤐", "Zipper-Mouth Face", []string{"secret", "quiet", "zip"}},
	{"🥴", "Woozy Face", []string{"drunk", "woozy"}},
	{"🤢", "Nauseated Face", []string{"sick", "gross", "vomit"}},
	{"🤮", "Face Vomiting", []string{"sick", "vomit"}},
	{"🤧", "Sneezing Face", []string{"sick", "sneeze", "cold"}},
	{"😷", "Face with Medical Mask", []string{"sick", "mask"}},
	{"🤒", "Face with Thermometer", []string{"sick", "fever"}},
	{"🤕", "Face with Head-Bandage", []string{"sick", "hurt"}},
	{"🤑", "Money-Mouth Face", []string{"money", "rich"}},
	{"🤠", "Cowboy Hat Face", []string{"cowboy", "yeehaw"}},
	{"😈", "Smiling Face with Horns", []string{"devil", "evil", "mischief"}},
	{"👿", "Angry Face with Horns", []string{"devil", "evil", "angry"}},
	{"💀", "Skull", []string{"dead", "skeleton", "death"}},
	{"👻", "Ghost", []string{"spooky", "halloween"}},
	{"alien", "Alien", []string{"ufo", "space"}},
	{"👾", "Alien Monster", []string{"retro", "game", "arcade"}},
	{"🤖", "Robot", []string{"bot", "computer", "tech"}},
	{"💩", "Pile of Poop", []string{"poop", "shit", "crap"}},

	// Gestures & Body
	{"👋", "Waving Hand", []string{"hello", "bye", "wave"}},
	{"🤚", "Raised Back of Hand", []string{"hand"}},
	{"✋", "Raised Hand", []string{"stop", "hand", "highfive"}},
	{"👌", "OK Hand", []string{"ok", "okay", "fine", "good"}},
	{"🤌", "Pinched Fingers", []string{"italian", "what"}},
	{"✌️", "Victory Hand", []string{"victory", "peace", "two"}},
	{"🤞", "Crossed Fingers", []string{"luck", "hope"}},
	{"🤟", "Love-You Gesture", []string{"love", "rock"}},
	{"🤘", "Sign of the Horns", []string{"rockon", "metal"}},
	{"🤙", "Call Me Hand", []string{"call", "phone"}},
	{"👈", "Backhand Index Pointing Left", []string{"left", "point"}},
	{"👉", "Backhand Index Pointing Right", []string{"right", "point"}},
	{"👆", "Backhand Index Pointing Up", []string{"up", "point"}},
	{"👇", "Backhand Index Pointing Down", []string{"down", "point"}},
	{"👍", "Thumbs Up", []string{"yes", "ok", "good", "agree", "like", "approve"}},
	{"👎", "Thumbs Down", []string{"no", "dislike", "disagree"}},
	{"✊", "Raised Fist", []string{"power", "fist"}},
	{"👊", "Oncoming Fist", []string{"punch", "fist"}},
	{"🤛", "Left-Facing Fist", []string{"fist"}},
	{"🤜", "Right-Facing Fist", []string{"fist"}},
	{"👏", "Clapping Hands", []string{"clap", "bravo", "goodjob"}},
	{"🙌", "Raising Hands", []string{"celebrate", "hooray"}},
	{"👐", "Open Hands", []string{"open", "hug"}},
	{"🤲", "Palms Up Together", []string{"prayer", "book"}},
	{"🤝", "Handshake", []string{"agree", "deal", "shake"}},
	{"🙏", "Folded Hands", []string{"please", "thankyou", "pray"}},
	{"✍️", "Writing Hand", []string{"write", "edit", "pen"}},
	{"💅", "Nail Polish", []string{"beauty", "glam"}},
	{"Selfie", "Selfie", []string{"phone", "camera"}},
	{"💪", "Flexed Biceps", []string{"strong", "muscle", "power"}},
	{"🧠", "Brain", []string{"mind", "smart", "think"}},
	{"👀", "Eyes", []string{"look", "see", "watch", "glance"}},
	{"👁️", "Eye", []string{"look", "see"}},
	{"👄", "Mouth", []string{"lip", "talk"}},
	{"💋", "Kiss Mark", []string{"kiss", "love"}},

	// Hearts
	{"❤️", "Red Heart", []string{"love", "like", "heart"}},
	{"🧡", "Orange Heart", []string{"love", "heart"}},
	{"💛", "Yellow Heart", []string{"love", "heart"}},
	{"💚", "Green Heart", []string{"love", "heart"}},
	{"💙", "Blue Heart", []string{"love", "heart"}},
	{"💜", "Purple Heart", []string{"love", "heart"}},
	{"🖤", "Black Heart", []string{"love", "heart"}},
	{"🤍", "White Heart", []string{"love", "heart"}},
	{"🤎", "Brown Heart", []string{"love", "heart"}},
	{"💔", "Broken Heart", []string{"sad", "breakup"}},

	// Activities & Tech & Miscellaneous
	{"🚀", "Rocket", []string{"fast", "launch", "ship", "space", "fly"}},
	{"💻", "Laptop", []string{"computer", "tech", "code", "dev"}},
	{"💡", "Light Bulb", []string{"idea", "light", "smart"}},
	{"🔥", "Fire", []string{"hot", "cool", "lit", "awesome", "burn"}},
	{"🎉", "Partying Face", []string{"celebrate", "party", "congrats", "tada", "event"}},
	{"✨", "Sparkles", []string{"shiny", "clean", "magic", "sparkle"}},
	{"🌟", "Glowing Star", []string{"star", "gold"}},
	{"⭐", "Star", []string{"star"}},
	{"🌈", "Rainbow", []string{"rainbow", "color"}},
	{"☀️", "Sun", []string{"sun", "warm", "day"}},
	{"🌧️", "Cloud with Rain", []string{"rain", "weather", "wet"}},
	{"❄️", "Snowflake", []string{"snow", "cold", "winter"}},
	{"⚡", "High Voltage", []string{"lightning", "bolt", "power", "electricity"}},
	{"🍀", "Four Leaf Clover", []string{"luck", "clover"}},
	{"🍁", "Maple Leaf", []string{"autumn", "leaf"}},
	{"🍕", "Pizza", []string{"food", "pizza"}},
	{"🍔", "Hamburger", []string{"food", "burger"}},
	{"🍺", "Beer Mug", []string{"drink", "beer"}},
	{"☕", "Hot Beverage", []string{"coffee", "tea", "drink"}},
	{"🎮", "Video Game", []string{"game", "play", "controller"}},
	{"🎵", "Musical Note", []string{"music", "song"}},
	{"📚", "Books", []string{"book", "read", "library"}},
	{"💼", "Briefcase", []string{"work", "business"}},
	{"🔒", "Locked", []string{"secure", "lock"}},
	{"🔑", "Key", []string{"secure", "key"}},
	{"🛒", "Shopping Cart", []string{"shop", "buy"}},
	{"📢", "Loudspeaker", []string{"announce", "shout"}},
	{"🔔", "Bell", []string{"notify", "alert"}},
	{"🏁", "Chequered Flag", []string{"finish", "race"}},
	{"🏆", "Trophy", []string{"win", "prize"}},
	{"✅", "Check Mark Button", []string{"done", "check", "yes", "success"}},
	{"❌", "Cross Mark", []string{"no", "cross", "cancel", "error"}},
	{"⚠️", "Warning", []string{"warn", "alert"}},
}

func (s *SmartState) resolveEmojiQuery() {
	q := strings.ToLower(strings.TrimSpace(s.query))
	if q == "" {
		s.emojiMatches = popularEmojis
		s.emojiIdx = 0
		return
	}

	var matches []EmojiInfo
	// 1. Exact/prefix match on name
	for _, emo := range emojiDatabase {
		nameLower := strings.ToLower(emo.Name)
		if strings.HasPrefix(nameLower, q) || strings.ReplaceAll(nameLower, " ", "") == q {
			matches = append(matches, emo)
		}
	}

	// Track matches we have already found
	seen := make(map[string]bool)
	for _, m := range matches {
		seen[m.Char] = true
	}

	// 2. Substring match on name or tag matches
	for _, emo := range emojiDatabase {
		if seen[emo.Char] {
			continue
		}
		nameLower := strings.ToLower(emo.Name)
		if strings.Contains(nameLower, q) {
			matches = append(matches, emo)
			seen[emo.Char] = true
			continue
		}
		for _, tag := range emo.Tags {
			if strings.HasPrefix(strings.ToLower(tag), q) || strings.ToLower(tag) == q {
				matches = append(matches, emo)
				seen[emo.Char] = true
				break
			}
		}
	}

	s.emojiMatches = matches
	s.emojiIdx = 0
}
