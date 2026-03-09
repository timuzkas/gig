package main

import (
	"github.com/diamondburned/gotk4/pkg/cairo"
	"math"
)

type GraphLane struct {
	Color [3]float64
	Hash  string
}

type CommitGraphInfo struct {
	Lanes      []GraphLane
	PrevLanes  []GraphLane
	CommitLane int
	Parents    []int
}

func GraphColor(index int) [3]float64 {
	colors := [][3]float64{
		{0.79, 0.58, 0.36},
		{0.48, 0.72, 0.48},
		{0.44, 0.62, 0.81},
		{0.75, 0.56, 0.75},
		{0.85, 0.44, 0.44},
		{0.44, 0.75, 0.75},
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

		prevLanes := make([]GraphLane, len(activeLanes))
		for j, hash := range activeLanes {
			prevLanes[j] = GraphLane{Hash: hash, Color: GraphColor(j)}
		}
		infos[i].PrevLanes = prevLanes

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

		parentLanes := []int{}
		if len(c.Parents) == 0 {
			activeLanes[laneIdx] = ""
		} else {
			activeLanes[laneIdx] = c.Parents[0]
			parentLanes = append(parentLanes, laneIdx)

			for j := 1; j < len(c.Parents); j++ {
				pHash := c.Parents[j]
				found := -1
				for k, hash := range activeLanes {
					if hash == pHash {
						found = k
						break
					}
				}
				if found == -1 {
					found = len(activeLanes)
					activeLanes = append(activeLanes, pHash)
				}
				parentLanes = append(parentLanes, found)
			}
		}
		infos[i].Parents = parentLanes

		lanes := make([]GraphLane, len(activeLanes))
		for j, hash := range activeLanes {
			lanes[j] = GraphLane{Hash: hash, Color: GraphColor(j)}
		}
		infos[i].Lanes = lanes
	}

	return infos
}

func DrawGraph(cr *cairo.Context, info CommitGraphInfo, width, height float64) {
	laneWidth := 14.0
	dotRadius := 3.5
	centerX := laneWidth / 2.0
	midY := math.Floor(height / 2.0)

	lx := func(lane int) float64 {
		return math.Floor(centerX+float64(lane)*laneWidth) + 0.5
	}

	seg := func(x0, y0, x1, y1 float64) {
		cr.MoveTo(x0, y0)
		if x0 == x1 {
			cr.LineTo(x1, y1)
		} else {
			half := (y0 + y1) / 2
			cr.CurveTo(x0, half, x1, half, x1, y1)
		}
		cr.Stroke()
	}

	cr.SetLineWidth(1.5)
	cr.SetLineJoin(cairo.LineJoinRound)
	cr.SetLineCap(cairo.LineCapRound)

	commitLane := info.CommitLane

	for i, prev := range info.PrevLanes {
		if prev.Hash == "" {
			continue
		}
		x0 := lx(i)
		cr.SetSourceRGB(prev.Color[0], prev.Color[1], prev.Color[2])

		if i == commitLane {
			seg(x0, 0, x0, midY)
			continue
		}

		destLane := -1
		for j, curr := range info.Lanes {
			if curr.Hash == prev.Hash {
				destLane = j
				break
			}
		}
		if destLane != -1 {
			seg(x0, 0, lx(destLane), height)
		}
	}

	if commitLane >= 0 {
		xDot := lx(commitLane)
		for _, pLane := range info.Parents {
			col := GraphColor(pLane)
			cr.SetSourceRGB(col[0], col[1], col[2])
			seg(xDot, midY, lx(pLane), height)
		}
	}

	if commitLane >= 0 && commitLane < len(info.Lanes) {
		x := lx(commitLane)
		y := midY
		color := GraphColor(commitLane)

		cr.SetSourceRGB(color[0], color[1], color[2])
		cr.Arc(x, y, dotRadius, 0, 2*math.Pi)
		cr.Fill()

		cr.SetSourceRGB(0.1, 0.1, 0.1)
		cr.Arc(x, y, 1.2, 0, 2*math.Pi)
		cr.Fill()
	}
}
