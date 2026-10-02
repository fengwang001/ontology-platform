package ontology

// appendRecord adds one record's entries to the column and updates pages
// and stats. It is called while holding the Shredder write lock.
func (c *column) appendRecord(es []Entry, recIdx, pageEntries int) {
	if c.openPage == nil {
		c.openPage = &PageInfo{StartRecord: recIdx}
	} else if c.openPage.EntryCount >= pageEntries {
		c.pages = append(c.pages, *c.openPage)
		c.openPage = &PageInfo{StartRecord: recIdx}
	}
	c.openPage.RecordCount++
	c.openPage.EntryCount += len(es)
	c.entries = append(c.entries, es...)

	for _, e := range es {
		if e.Null {
			c.nulls++
			continue
		}
		c.present++
		if !c.hasPresent {
			c.hasPresent = true
			c.min, c.max = e.Value, e.Value
		} else if e.Value < c.min {
			c.min = e.Value
		} else if e.Value > c.max {
			c.max = e.Value
		}
	}
}
