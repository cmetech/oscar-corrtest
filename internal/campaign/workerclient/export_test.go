package workerclient

// SetMaxStream lowers the total ledger size cap for tests.
func (c *Client) SetMaxStream(n int) { c.maxStream = n }
