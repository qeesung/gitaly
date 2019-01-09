package chunker

type Item interface{}

type Response interface {
	Append(Item)
	Send() error
}

type Sender struct {
	NewResponse func() Response

	n        int
	response Response
}

const chunkSize = 20

func (c *Sender) Send(it Item) error {
	if c.n == 0 {
		c.response = c.NewResponse()
	}

	c.response.Append(it)
	c.n++

	if c.n >= chunkSize {
		return c.send()
	}
	return nil
}

func (c *Sender) send() error {
	c.n = 0
	return c.response.Send()
}

func (c *Sender) Flush() error {
	if c.n == 0 {
		return nil
	}

	return c.send()
}
