def run!(cmd, chdir='.')
  GitalySupport.print_cmd(cmd)
  unless system(*cmd, chdir: chdir)
    GitalySupport.fail_cmd!(cmd)
  end
end

def run2!(cmd, chdir: '.', out: 1)
  GitalySupport.print_cmd(cmd)
  unless system(*cmd, chdir: chdir, out: out)
    GitalySupport.fail_cmd!(cmd)
  end
end

def capture!(cmd, chdir='.')
  GitalySupport.print_cmd(cmd)
  output = IO.popen(cmd, chdir: chdir) { |io| io.read }
  GitalySupport.fail_cmd!(cmd) unless $?.success?
  output
end

module GitalySupport
  class << self
    def print_cmd(cmd)
      puts '-> ' + printable_cmd(cmd)
    end

    def fail_cmd!(cmd)
      abort "command failed: #{printable_cmd(cmd)}"
    end

    def printable_cmd(cmd)
      cmd.join(' ')
    end
  end
end
