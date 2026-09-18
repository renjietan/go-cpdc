package output

import "strings"

type iniUpdate struct {
	section string
	key     string
	value   string
}

func updateINI(existing []byte, updates []iniUpdate) []byte {
	text := strings.ReplaceAll(string(existing), "\r\n", "\n")
	text = strings.TrimSuffix(text, "\n")
	var lines []string
	if text != "" {
		lines = strings.Split(text, "\n")
	}
	for _, update := range updates {
		lines = setINIValue(lines, update)
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func setINIValue(lines []string, update iniUpdate) []string {
	sectionStart, sectionEnd := -1, len(lines)
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) >= 2 && strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			if sectionStart >= 0 {
				sectionEnd = index
				break
			}
			if strings.EqualFold(section, update.section) {
				sectionStart = index
			}
		}
	}
	if sectionStart < 0 {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		return append(lines, "["+update.section+"]", update.key+"="+update.value)
	}
	for index := sectionStart + 1; index < sectionEnd; index++ {
		line := strings.TrimSpace(lines[index])
		equals := strings.IndexByte(line, '=')
		if equals < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(line[:equals]), update.key) {
			lines[index] = update.key + "=" + update.value
			return lines
		}
	}
	lines = append(lines, "")
	copy(lines[sectionEnd+1:], lines[sectionEnd:])
	lines[sectionEnd] = update.key + "=" + update.value
	return lines
}
