// Package restoretest provides a stand-in for the language model of a
// restore, for tests: it works through the tools the way a model would
// and answers without reaching any service.
package restoretest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"github.com/docker/docker-agent/pkg/chat"
	"github.com/docker/docker-agent/pkg/model/provider/contracts"
	"github.com/docker/docker-agent/pkg/modelsdev"
	"github.com/docker/docker-agent/pkg/tools"

	"github.com/manzanit0/athenaeum/internal/restore"
)

// Patterns of what the stand-in reads in a conversation.
var (
	// asked matches the pages a session is asked for.
	asked = regexp.MustCompile(`[Pp]ages (\d+) to (\d+)`)
	// missing matches the pages a session is asked for once more.
	missing = regexp.MustCompile(`^Pages ([\d, ]+) of the book are not saved`)
	// shown matches the start of what read_pages says about a page.
	shown = regexp.MustCompile(`(?m)^Page (\d+) (has these lines of text:|is a scan|is blank|cannot be shown)`)
	// noted matches the note a line set large starts with.
	noted = regexp.MustCompile(`^\(size (\d+)\) `)
	// listed matches one line of the list of headings.
	listed = regexp.MustCompile(`(?m)^(h\d+) \| \d+ \| (\d+) \| (.+)$`)
	// stretched matches one line of the list of title lines and
	// stretches set in capitals.
	stretched = regexp.MustCompile(`(?m)^([tc]\d+) \| \d+ \| (.+)$`)
)

// Model stands in for a language model. Asked for pages, it reads them
// and saves a page with text as the text it was shown, a line set large
// becoming a heading and the largest the title of the book, and a scan as a heading and a paragraph that name
// the page. Asked to standardise headings, it writes them with one
// capital at the start.
type Model struct {
	// Name is the name the stand-in goes by; empty means "stand-in".
	Name string
	// Decline, when set, is why the stand-in says after the sample that
	// the book cannot be restored well.
	Decline string
	// Skip lists pages the stand-in never saves.
	Skip []int
	// MostSaves, when set, is how many pages the stand-in can save in
	// one answer: an answer that saves more is cut off, the way one that
	// grows too long is. Below zero, every answer that saves is cut off.
	MostSaves int
	// Tokens is how many tokens every answer is said to have read and
	// written.
	Tokens int64
	// mu guards calls.
	mu sync.Mutex
	// calls counts the answers given.
	calls int
}

// Calls returns how many answers the stand-in has given.
func (m *Model) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// ID names the stand-in.
func (m *Model) ID() modelsdev.ID {
	if m.Name != "" {
		return modelsdev.NewID("anthropic", m.Name)
	}
	return modelsdev.NewID("anthropic", "stand-in")
}

// BaseConfig returns an empty configuration.
func (m *Model) BaseConfig() contracts.Config {
	return contracts.Config{}
}

// CreateChatCompletionStream answers the conversation held so far.
func (m *Model) CreateChatCompletionStream(_ context.Context, messages []chat.Message, kit []tools.Tool) (chat.MessageStream, error) {
	m.mu.Lock()
	m.calls++
	number := m.calls
	m.mu.Unlock()
	calls := m.next(messages, kit)
	for i := range calls {
		calls[i].ID = fmt.Sprintf("call-%d-%d", number, i)
		calls[i].Type = "function"
	}
	saves := 0
	for _, c := range calls {
		if c.Function.Name == restore.ToolSavePage {
			saves++
		}
	}
	cut := m.MostSaves != 0 && saves > max(m.MostSaves, 0)
	return &stream{calls: calls, tokens: m.Tokens, cut: cut}, nil
}

// call returns a call of the tool called name with the given arguments.
func call(name string, args any) tools.ToolCall {
	data, _ := json.Marshal(args)
	return tools.ToolCall{Function: tools.FunctionCall{Name: name, Arguments: string(data)}}
}

// offered reports whether the tool called name is among kit.
func offered(kit []tools.Tool, name string) bool {
	for _, tool := range kit {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// next returns the tool calls the stand-in makes next, or none when its
// work is done.
func (m *Model) next(messages []chat.Message, kit []tools.Tool) []tools.ToolCall {
	var prompt string
	var results []string
	made := map[string]int{}
	saved := map[int]bool{}
	for _, msg := range messages {
		switch msg.Role {
		case chat.MessageRoleUser:
			prompt = msg.Content
		case chat.MessageRoleAssistant:
			for _, c := range msg.ToolCalls {
				made[c.Function.Name]++
				var args restore.SaveArgs
				if c.Function.Name == restore.ToolSavePage && json.Unmarshal([]byte(c.Function.Arguments), &args) == nil {
					saved[args.Page] = true
				}
			}
		case chat.MessageRoleTool:
			text := msg.Content
			for _, part := range msg.MultiContent {
				if part.Type == chat.MessagePartTypeText {
					text = part.Text
				}
			}
			results = append(results, text)
		}
	}
	if offered(kit, restore.ToolSetHeadings) {
		var calls []tools.ToolCall
		if headings := standardised(prompt); len(headings) > 0 && made[restore.ToolSetHeadings] == 0 {
			calls = append(calls, call(restore.ToolSetHeadings, restore.HeadingsArgs{Headings: headings}))
		}
		if lines := recased(prompt); len(lines) > 0 && made[restore.ToolSetCapitals] == 0 {
			calls = append(calls, call(restore.ToolSetCapitals, restore.CapitalsArgs{Lines: lines}))
		}
		return calls
	}
	var wanted []int
	if match := missing.FindStringSubmatch(prompt); match != nil {
		for _, word := range strings.Split(match[1], ",") {
			if page, err := strconv.Atoi(strings.TrimSpace(word)); err == nil {
				wanted = append(wanted, page)
			}
		}
	} else if match := asked.FindStringSubmatch(prompt); match != nil {
		first, _ := strconv.Atoi(match[1])
		last, _ := strconv.Atoi(match[2])
		for page := first; page <= last; page++ {
			wanted = append(wanted, page)
		}
	}
	pages := map[int]string{}
	for _, result := range results {
		starts := shown.FindAllStringSubmatchIndex(result, -1)
		for i, at := range starts {
			page, _ := strconv.Atoi(result[at[2]:at[3]])
			end := len(result)
			if i+1 < len(starts) {
				end = starts[i+1][0]
			}
			pages[page] = result[at[0]:end]
		}
	}
	for _, page := range wanted {
		if _, read := pages[page]; !read {
			return []tools.ToolCall{call(restore.ToolReadPages, restore.ReadArgs{From: page, To: page})}
		}
	}
	var calls []tools.ToolCall
	for _, page := range wanted {
		if !saved[page] && !m.skips(page) {
			calls = append(calls, call(restore.ToolSavePage, transcribed(page, pages[page])))
		}
	}
	if len(calls) > 1 && strings.Contains(prompt, "one page to an answer") {
		calls = calls[:1]
	}
	if len(calls) > 0 {
		return calls
	}
	if offered(kit, restore.ToolJudgeSample) && made[restore.ToolJudgeSample] == 0 {
		reason := "The pages read cleanly."
		if m.Decline != "" {
			reason = m.Decline
		}
		return []tools.ToolCall{call(restore.ToolJudgeSample, restore.JudgeArgs{Good: m.Decline == "", Reason: reason})}
	}
	return nil
}

// skips reports whether the stand-in never saves the given page.
func (m *Model) skips(page int) bool {
	for _, skipped := range m.Skip {
		if skipped == page {
			return true
		}
	}
	return false
}

// transcribed returns the transcription the stand-in makes of a page
// from what read_pages said about it.
func transcribed(page int, said string) restore.SaveArgs {
	args := restore.SaveArgs{Page: page, Blocks: []restore.Block{}}
	head, body, _ := strings.Cut(said, "\n")
	switch {
	case strings.Contains(head, "is a scan"):
		args.Blocks = append(args.Blocks,
			restore.Block{Kind: "heading", Level: 1, Text: fmt.Sprintf("CHAPTER %d", page)},
			restore.Block{Kind: "paragraph", Text: fmt.Sprintf("The words read from the scan of page %d.[^1]", page)},
			restore.Block{Kind: "note", Marker: "1", Text: fmt.Sprintf("A note on page %d.", page)})
		args.Doubts = []restore.Doubt{{Text: "scan", Reason: "The word is smudged."}}
	case strings.Contains(head, "has these lines"):
		var text []string
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if note := noted.FindStringSubmatch(line); note != nil {
				// The largest lines are the title of the book, the others headings.
				block := restore.Block{Kind: "heading", Level: 1, Text: line[len(note[0]):]}
				if size, _ := strconv.Atoi(note[1]); size >= 24 {
					block = restore.Block{Kind: "title", Text: line[len(note[0]):]}
				}
				args.Blocks = append(args.Blocks, block)
				continue
			}
			text = append(text, line)
		}
		if len(text) > 0 {
			args.Blocks = append(args.Blocks, restore.Block{Kind: "paragraph", Text: strings.Join(text, " ")})
		}
	}
	return args
}

// sentence returns text in lower case with one capital at its start.
func sentence(text string) string {
	out := []rune(strings.ToLower(text))
	out[0] = unicode.ToUpper(out[0])
	return string(out)
}

// standardised returns the headings listed in prompt with one capital
// at the start of each. A heading with no text under it, when another
// follows, is no heading to the stand-in.
func standardised(prompt string) []restore.Heading {
	var out []restore.Heading
	for _, match := range listed.FindAllStringSubmatchIndex(prompt, -1) {
		level, _ := strconv.Atoi(prompt[match[4]:match[5]])
		if strings.HasPrefix(prompt[match[1]:], "\n    no text before the next heading\nh") {
			level = 0
		}
		out = append(out, restore.Heading{ID: prompt[match[2]:match[3]], Level: level, Text: sentence(prompt[match[6]:match[7]])})
	}
	return out
}

// recased returns the title lines and stretches listed in prompt with
// one capital at the start of each.
func recased(prompt string) []restore.Line {
	var out []restore.Line
	for _, match := range stretched.FindAllStringSubmatch(prompt, -1) {
		out = append(out, restore.Line{ID: match[1], Text: sentence(match[2])})
	}
	return out
}

// stream hands out one answer.
type stream struct {
	// calls holds the tool calls of the answer; none means the answer
	// is the word that ends a session.
	calls []tools.ToolCall
	// tokens is how many tokens the answer is said to have read and
	// written.
	tokens int64
	// cut reports whether the answer is cut off for being too long.
	cut bool
	// sent reports whether the answer has been handed out.
	sent bool
}

// Recv returns the answer, and then the end of the stream.
func (s *stream) Recv() (chat.MessageStreamResponse, error) {
	if s.sent {
		return chat.MessageStreamResponse{}, io.EOF
	}
	s.sent = true
	choice := chat.MessageStreamChoice{
		Delta:        chat.MessageDelta{Role: string(chat.MessageRoleAssistant), ToolCalls: s.calls},
		FinishReason: chat.FinishReasonToolCalls,
	}
	if len(s.calls) == 0 {
		choice.Delta.Content = "Done"
		choice.FinishReason = chat.FinishReasonStop
	}
	if s.cut {
		choice.FinishReason = chat.FinishReasonLength
	}
	return chat.MessageStreamResponse{
		Choices: []chat.MessageStreamChoice{choice},
		Usage:   &chat.Usage{InputTokens: s.tokens, OutputTokens: s.tokens},
	}, nil
}

// Close does nothing: the stream holds nothing to release.
func (s *stream) Close() {}
