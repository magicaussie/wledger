package spotlight

import (
    "fmt"
    "testing"
)

func TestSpotlightURLExtraction(t *testing.T) {
    p := NewProvider()
    
    urls := []string{
        "https://www.spotlightstores.com/en-au/p/gingham-check-120-cm-multipurpose-cotton-fabric/BP80558787-sage",
        "https://spotlightstores.com/en-au/p/plain-112-cm-cotton-drill-fabric/BP80071782001-black",
        "https://www.spotlightstores.com/search?text=fabric",
    }
    
    for _, u := range urls {
        id, ok := p.ExtractPartIDFromURL(u)
        fmt.Printf("URL: %s\n  -> ID: %q, ok: %v\n", u, id, ok)
    }
}
