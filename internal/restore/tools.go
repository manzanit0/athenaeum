package restore

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/docker/docker-agent/pkg/tools"
)

// The names of the tools the model works with.
const (
	// ToolReadPages shows pages of the book.
	ToolReadPages = "read_pages"
	// ToolSavePage keeps the transcription of a page.
	ToolSavePage = "save_page"
	// ToolJudgeSample says whether the book can be restored well.
	ToolJudgeSample = "judge_sample"
	// ToolSetHeadings gives headings their level and wording.
	ToolSetHeadings = "set_headings"
	// ToolSetCapitals gives stretches of text the capitals grammar asks
	// for.
	ToolSetCapitals = "set_capitals"
)

// maxShown is the most pages shown by one call.
const maxShown = 5

// batch is a run of pages one session of the model works through.
type batch struct {
	// first is the first page, counted from one.
	first int
	// last is the last page.
	last int
	// sample reports whether the batch is the sample the model judges
	// the book by.
	sample bool
}

// ReadArgs asks for pages to be shown.
type ReadArgs struct {
	// From is the first page.
	From int `json:"from" jsonschema:"the first page to show"`
	// To is the last page.
	To int `json:"to" jsonschema:"the last page to show, at most four pages after the first"`
}

// SaveArgs holds the transcription of a page.
type SaveArgs struct {
	// Page is the number of the page.
	Page int `json:"page" jsonschema:"the number of the page"`
	// Blocks holds the text of the page in reading order.
	Blocks []Block `json:"blocks" jsonschema:"the text of the page in reading order; empty for a page with nothing to read"`
	// Doubts lists what could not be read with certainty.
	Doubts []Doubt `json:"doubts,omitempty" jsonschema:"the passages that could not be read with certainty"`
}

// JudgeArgs says what the model makes of the sample.
type JudgeArgs struct {
	// Good reports whether the book can be restored well.
	Good bool `json:"good" jsonschema:"whether the rest of the book can be restored well"`
	// Reason says why, in a sentence.
	Reason string `json:"reason" jsonschema:"why, in one plain sentence the owner of the book can read"`
}

// Heading is the level and wording a heading is to have.
type Heading struct {
	// ID identifies the heading.
	ID string `json:"id" jsonschema:"the identifier of the heading, as listed"`
	// Level is the level of the heading.
	Level int `json:"level" jsonschema:"1 for a part or chapter, 2 for a section, 3 for a subsection, 0 when it is not a heading at all"`
	// Text is the wording of the heading.
	Text string `json:"text" jsonschema:"the heading with its capitals standardised and no other change to its words"`
}

// HeadingsArgs gives headings their level and wording.
type HeadingsArgs struct {
	// Headings lists the headings to change.
	Headings []Heading `json:"headings" jsonschema:"every heading of the book"`
}

// Line is the wording a stretch of text is to have.
type Line struct {
	// ID identifies the stretch.
	ID string `json:"id" jsonschema:"the identifier of the line, as listed"`
	// Text is the wording of the stretch.
	Text string `json:"text" jsonschema:"the listed text with the capitals grammar asks for and no other change to its words"`
}

// CapitalsArgs gives stretches of text their capitals.
type CapitalsArgs struct {
	// Lines lists the stretches to change.
	Lines []Line `json:"lines" jsonschema:"every line listed"`
}

// within reports what is wrong with asking for the given page in the
// batch b, or nil.
func (b batch) within(page int) error {
	if page < b.first || page > b.last {
		return fmt.Errorf("page %d is not yours to work on: stay within pages %d to %d", page, b.first, b.last)
	}
	return nil
}

// list words page numbers for a message.
func list(pages []int) string {
	words := make([]string, len(pages))
	for i, page := range pages {
		words[i] = fmt.Sprint(page)
	}
	return strings.Join(words, ", ")
}

// pageTools returns the tools of a session that transcribes the pages
// of b.
func (r *run) pageTools(b batch) []tools.Tool {
	out := []tools.Tool{
		{
			Name:     ToolReadPages,
			Category: "restore",
			Description: "Shows pages of the book: the picture of a scanned page, or the lines of a page that has text. " +
				"At most five pages at a time.",
			Parameters: tools.MustSchemaFor[ReadArgs](),
			Handler: tools.NewHandler(func(_ context.Context, args ReadArgs) (*tools.ToolCallResult, error) {
				return r.readPages(b, args), nil
			}),
		},
		{
			Name:        ToolSavePage,
			Category:    "restore",
			Description: "Keeps the transcription of one page. Saving a page again replaces what was kept for it.",
			Parameters:  tools.MustSchemaFor[SaveArgs](),
			Handler: tools.NewHandler(func(_ context.Context, args SaveArgs) (*tools.ToolCallResult, error) {
				return r.savePage(b, args), nil
			}),
		},
	}
	if b.sample {
		out = append(out, tools.Tool{
			Name:     ToolJudgeSample,
			Category: "restore",
			Description: "Says whether the rest of the book can be restored well, judging by the sample. " +
				"Saying it cannot ends the restore before any more is spent on it.",
			Parameters: tools.MustSchemaFor[JudgeArgs](),
			Handler: tools.NewHandler(func(_ context.Context, args JudgeArgs) (*tools.ToolCallResult, error) {
				r.draft.judge(args.Good, args.Reason)
				if !args.Good {
					return tools.ResultSuccess("Noted. The restore ends here. Do not call any more tools."), nil
				}
				return tools.ResultSuccess("Noted. The rest of the book will be restored."), nil
			}),
		})
	}
	return out
}

// readPages shows the pages asked for.
func (r *run) readPages(b batch, args ReadArgs) *tools.ToolCallResult {
	if args.To < args.From {
		args.To = args.From
	}
	if err := b.within(args.From); err != nil {
		return tools.ResultError(err.Error())
	}
	if err := b.within(args.To); err != nil {
		return tools.ResultError(err.Error())
	}
	if args.To-args.From >= maxShown {
		return tools.ResultError(fmt.Sprintf("that is more than %d pages at once: ask for fewer", maxShown))
	}
	result := &tools.ToolCallResult{}
	var sb strings.Builder
	for page := args.From; page <= args.To; page++ {
		shown, err := r.source.show(page)
		switch {
		case err != nil || shown.kind == unreadablePage:
			fmt.Fprintf(&sb, "Page %d cannot be shown. Save it without blocks.\n\n", page)
		case shown.kind == blankPage:
			fmt.Fprintf(&sb, "Page %d is blank. Save it without blocks.\n\n", page)
		case shown.kind == textPage:
			fmt.Fprintf(&sb, "Page %d has these lines of text:\n%s\n", page, shown.text)
		default:
			result.Images = append(result.Images, tools.MediaContent{
				Data: base64.StdEncoding.EncodeToString(shown.image), MimeType: shown.mime,
			})
			fmt.Fprintf(&sb, "Page %d is a scan: picture %d of those attached.\n\n", page, len(result.Images))
		}
	}
	result.Output = sb.String()
	return result
}

// savePage keeps the transcription of a page.
func (r *run) savePage(b batch, args SaveArgs) *tools.ToolCallResult {
	if err := b.within(args.Page); err != nil {
		return tools.ResultError(err.Error())
	}
	for i := range args.Doubts {
		args.Doubts[i].Page = args.Page
	}
	if err := r.draft.save(args.Page, args.Blocks, args.Doubts); err != nil {
		return tools.ResultError(fmt.Sprintf("Page %d was not saved: %v.", args.Page, err))
	}
	r.report()
	if left := r.draft.missing(b.first, b.last); len(left) > 0 {
		return tools.ResultSuccess(fmt.Sprintf("Saved page %d. Still to save: %s.", args.Page, list(left)))
	}
	if b.sample && r.draft.judged() == nil {
		return tools.ResultSuccess(fmt.Sprintf("Saved page %d. Every page of the sample is saved: now call %s.", args.Page, ToolJudgeSample))
	}
	return tools.ResultSuccess(fmt.Sprintf("Saved page %d. Every page is saved: you are done.", args.Page))
}

// headingTools returns the tools of the session that standardises the
// headings and the capitals. stretches holds what the capitals of are
// looked at.
func (r *run) headingTools(stretches []stretch) []tools.Tool {
	byID := map[string]stretch{}
	for _, s := range stretches {
		byID[s.id] = s
	}
	capitals := tools.Tool{
		Name:     ToolSetCapitals,
		Category: "restore",
		Description: "Gives lines of the title page and stretches of text set in capitals the capitals grammar asks for. " +
			"Only capitals, accents and emphasis may change; a change to the words is turned down.",
		Parameters: tools.MustSchemaFor[CapitalsArgs](),
		Handler: tools.NewHandler(func(_ context.Context, args CapitalsArgs) (*tools.ToolCallResult, error) {
			var refused []string
			for _, line := range args.Lines {
				s, ok := byID[strings.TrimSpace(line.ID)]
				if !ok {
					refused = append(refused, fmt.Sprintf("%s: there is no such line", line.ID))
				} else if err := r.draft.recase(s, line.Text); err != nil {
					refused = append(refused, err.Error())
				}
			}
			if len(refused) > 0 {
				return tools.ResultError(fmt.Sprintf("%d of %d lines were set. These were turned down:\n%s",
					len(args.Lines)-len(refused), len(args.Lines), strings.Join(refused, "\n"))), nil
			}
			return tools.ResultSuccess(fmt.Sprintf("All %d lines were set.", len(args.Lines))), nil
		}),
	}
	return []tools.Tool{capitals, {
		Name:     ToolSetHeadings,
		Category: "restore",
		Description: "Gives headings their level and wording. Only the capitals, accents, spacing and punctuation of a " +
			"heading may change; a change to its words is turned down. Level 0 turns a heading into ordinary text.",
		Parameters: tools.MustSchemaFor[HeadingsArgs](),
		Handler: tools.NewHandler(func(_ context.Context, args HeadingsArgs) (*tools.ToolCallResult, error) {
			var refused []string
			for _, h := range args.Headings {
				if err := r.draft.retitle(strings.TrimSpace(h.ID), h.Level, h.Text); err != nil {
					refused = append(refused, err.Error())
				}
			}
			if len(refused) > 0 {
				return tools.ResultError(fmt.Sprintf("%d of %d headings were set. These were turned down:\n%s",
					len(args.Headings)-len(refused), len(args.Headings), strings.Join(refused, "\n"))), nil
			}
			return tools.ResultSuccess(fmt.Sprintf("All %d headings were set.", len(args.Headings))), nil
		}),
	}}
}
