package scanner

import "testing"

func TestParseLighthouseScores(t *testing.T) {
	report := []byte(`{"categories":{
		"performance":{"score":1},
		"accessibility":{"score":0.9},
		"best-practices":{"score":0.89},
		"seo":{"score":1}
	}}`)
	scores, err := ParseLighthouseScores(report)
	if err != nil {
		t.Fatalf("ParseLighthouseScores returned error: %v", err)
	}
	if !scores.HasAnyScore() {
		t.Fatalf("expected HasAnyScore to be true for a populated report")
	}
	if scores.Performance == nil || *scores.Performance != 100 {
		t.Fatalf("expected performance 100, got %v", scores.Performance)
	}
	if scores.BestPractices == nil || *scores.BestPractices != 89 {
		t.Fatalf("expected best-practices 89, got %v", scores.BestPractices)
	}
	// Health is the average of the four available categories.
	if scores.Health == nil || *scores.Health != (100+90+89+100)/4.0 {
		t.Fatalf("expected health to be the category average, got %v", scores.Health)
	}
}

func TestHasAnyScore(t *testing.T) {
	if (ScoreSet{}).HasAnyScore() {
		t.Fatalf("an empty ScoreSet should report no scores")
	}
	one := 42.0
	if !(ScoreSet{SEO: &one}).HasAnyScore() {
		t.Fatalf("a ScoreSet with one category should report a score")
	}
}
