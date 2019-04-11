// Command tools exists purely to ensure the package manager doesn't prune the
// CI tools from our vendor folder. This command is not meant for actual usage.
package main

func main() {
	panic("this command only exists to help vendor CI tools")
}
