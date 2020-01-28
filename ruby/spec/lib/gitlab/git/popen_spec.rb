require 'spec_helper'

describe 'Gitlab::Git::Popen' do
  let(:path) { Dir.mktmpdir }

  let(:klass) do
    Class.new(Object) do
      include Gitlab::Git::Popen
    end
  end

  after do
    FileUtils.remove_entry path
  end

  context 'popen' do
    context 'zero status' do
      let(:result) { klass.new.popen(%w(ls), path) }
      let(:status) { result.last }

      it { expect(status).to be_zero }
    end

    context 'non-zero status' do
      let(:result) { klass.new.popen(%w(cat NOTHING), path) }
      let(:output) { result.first }
      let(:status) { result.last }

      it { expect(status).to eq(1) }
      it { expect(output).to include('No such file or directory') }
    end

    context 'when stderr is not included' do
      let(:result) { klass.new.popen(%w(cat NOTHING), path, include_stderr: false) }
      let(:output) { result.first }
      let(:status) { result.last }

      it { expect(status).to eq(1) }
      it { expect(output).to eq('') }
    end

    context 'when stderr is included' do
      let(:result) { klass.new.popen(['ruby', '-e', 'warn "hello world"'], path, include_stderr: true) }
      let(:output) { result.first }
      let(:status) { result.last }

      it { expect(status).to eq(0) }
      it { expect(output).to eq("hello world\n") }
    end

    context 'unsafe string command' do
      it 'raises an error when it gets called with a string argument' do
        expect { klass.new.popen('ls', path) }.to raise_error(RuntimeError)
      end
    end

    context 'with custom options' do
      let(:vars) { { 'foobar' => 123, 'PWD' => path } }
      let(:options) { { chdir: path } }

      it 'calls popen3 with the provided environment variables' do
        expect(Open3).to receive(:popen3).with(vars, 'ls', options)

        klass.new.popen(%w(ls), path, 'foobar' => 123)
      end
    end

    context 'use stdin' do
      let(:result) { klass.new.popen(%w[cat], path) { |stdin| stdin.write 'hello' } }
      let(:output) { result.first }
      let(:status) { result.last }

      it { expect(status).to be_zero }
      it { expect(output).to eq('hello') }
    end

    context 'with lazy block' do
      it 'yields a lazy io' do
        expect_lazy_io = lambda do |io|
          expect(io).to be_a Enumerator::Lazy
          expect(io.inspect).to include('#<IO:fd')
        end

        klass.new.popen(%w[ls], path, lazy_block: expect_lazy_io)
      end

      it "doesn't wait for process exit" do
        Timeout.timeout(2) do
          klass.new.popen(%w[yes], path, lazy_block: ->(io) {})
        end
      end
    end

    context 'with non ASCII output' do
      let(:stdin) { StringIO.new }
      let(:stdout) { StringIO.new("Preparando \xC3\xA1rbol de trabajo") }
      let(:stderr) { StringIO.new("UTF-8 error é").set_encoding('UTF-8') }
      let(:process_status) { double('Process::Status', exitstatus: 0) }
      let(:wait_thr) { double('Process::Waiter', value: process_status) }

      it "handles the output correctly" do
        expect(Open3).to receive(:popen3).and_yield(stdin, stdout, stderr, wait_thr)

        klass.new.popen(%w[ls], path)
      end
    end
  end

  context 'popen_with_timeout' do
    let(:timeout) { 1.second }

    context 'no timeout' do
      context 'zero status' do
        let(:result) { klass.new.popen_with_timeout(%w(ls), timeout, path) }
        let(:status) { result.last }

        it { expect(status).to be_zero }
      end

      context 'non-zero status' do
        let(:result) { klass.new.popen_with_timeout(%w(cat NOTHING), timeout, path) }
        let(:output) { result.first }
        let(:status) { result.last }

        it { expect(status).to eq(1) }
        it { expect(output).to include('No such file or directory') }
      end

      context 'unsafe string command' do
        it 'raises an error when it gets called with a string argument' do
          expect { klass.new.popen_with_timeout('ls', timeout, path) }.to raise_error(RuntimeError)
        end
      end
    end

    context 'timeout' do
      context 'timeout' do
        it "raises a Timeout::Error" do
          expect { klass.new.popen_with_timeout(%w(sleep 1000), timeout, path) }.to raise_error(Timeout::Error)
        end

        it "handles processes that do not shutdown correctly" do
          expect { klass.new.popen_with_timeout(['bash', '-c', "trap -- '' SIGTERM; sleep 1000"], timeout, path) }.to raise_error(Timeout::Error)
        end
      end

      context 'timeout period' do
        let(:time_taken) do
          begin
            start = Time.now
            klass.new.popen_with_timeout(%w(sleep 1000), timeout, path)
          rescue
            Time.now - start
          end
        end

        it { expect(time_taken).to be >= timeout }
      end

      context 'clean up' do
        let(:instance) { klass.new }

        it 'kills the child process' do
          expect(instance).to receive(:kill_process_group_for_pid).and_wrap_original do |m, *args|
            # is the PID, and it should not be running at this point
            m.call(*args)

            pid = args.first
            begin
              Process.getpgid(pid)
              raise "The child process should have been killed"
            rescue Errno::ESRCH
            end
          end

          expect { instance.popen_with_timeout(['bash', '-c', "trap -- '' SIGTERM; sleep 1000"], timeout, path) }.to raise_error(Timeout::Error)
        end
      end
    end
  end
end
