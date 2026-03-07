package main

import (
	"github.com/diamondburned/gotk4/pkg/cairo"
)

type GraphLane struct {
	Color [3]float64
	Hash  string
}

type CommitGraphInfo struct {
	Lanes      []GraphLane
	CommitLane int
}

// GraphColor returns a consistent color for a given lane index
func GraphColor(index int) [3]float64 {
	colors := [][3]float64{
		{0.79, 0.58, 0.36}, // Orange
		{0.48, 0.72, 0.48}, // Green
		{0.44, 0.62, 0.81}, // Blue
		{0.75, 0.56, 0.75}, // Purple
		{0.85, 0.44, 0.44}, // Red
		{0.44, 0.75, 0.75}, // Cyan
	}
	return colors[index%len(colors)]
}

func ComputeGraphInfo(commits []Commit) []CommitGraphInfo {
	infos := make([]CommitGraphInfo, len(commits))
	activeLanes := []string{}

	for i, c := range commits {
		if !c.IsCommit {
			continue
		}

		laneIdx := -1
		for j, hash := range activeLanes {
			if hash == c.Hash {
				laneIdx = j
				break
			}
		}

		if laneIdx == -1 {
			laneIdx = len(activeLanes)
			activeLanes = append(activeLanes, c.Hash)
		}

		infos[i].CommitLane = laneIdx
		infos[i].Lanes = make([]GraphLane, len(activeLanes))
		for j, hash := range activeLanes {
			infos[i].Lanes[j] = GraphLane{
				Color: GraphColor(j),
				Hash:  hash,
			}
		}

		if len(c.Parents) > 0 {
			activeLanes[laneIdx] = c.Parents[0]
			for j := 1; j < len(c.Parents); j++ {
				pHash := c.Parents[j]
				exists := false
				for _, hash := range activeLanes {
					if hash == pHash {
						exists = true
						break
					}
				}
				if !exists {
					activeLanes = append(activeLanes, pHash)
				}
			}
		}
	}

	return infos
}

func DrawGraph(cr *cairo.Context, info CommitGraphInfo, width, height float64) {
	laneWidth := 14.0
	dotRadius := 3.5
	centerX := laneWidth / 2.0

	cr.SetLineWidth(1.5)

	// Draw lines for all active lanes
	for i, lane := range info.Lanes {
		x := centerX + float64(i)*laneWidth
		cr.SetSourceRGB(lane.Color[0], lane.Color[1], lane.Color[2])
		
		cr.MoveTo(x, 0)
		cr.LineTo(x, height)
		cr.Stroke()
	}

	// Draw the commit dot
	if info.CommitLane >= 0 && info.CommitLane < len(info.Lanes) {
		x := centerX + float64(info.CommitLane)*laneWidth
		y := height / 2.0
		color := info.Lanes[info.CommitLane].Color
		
		cr.SetSourceRGB(color[0], color[1], color[2])
		cr.Arc(x, y, dotRadius, 0, 2*3.14159)
		cr.Fill()
		
		// Inner dot for a "node" look
		cr.SetSourceRGB(0.1, 0.1, 0.1) // Match background approximately
		cr.Arc(x, y, 1.2, 0, 2*3.14159)
		cr.Fill()
	}
}
