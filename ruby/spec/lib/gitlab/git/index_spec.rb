require 'spec_helper'

describe Gitlab::Git::Index do
  include TestRepo

  let(:repository) { gitlab_git_from_gitaly(new_mutable_test_repo) }
  let(:index) { described_class.new(repository) }

  before do
    index.read_tree(lookup('with-executables').tree)
  end

  describe '#create' do
    let(:options) do
      {
        content: 'Lorem ipsum...',
        file_path: 'documents/story.txt'
      }
    end

    context 'when no file at that path exists' do
      it 'creates the file in the index' do
        index.create(options)

        entry = index.get(options[:file_path])

        expect(entry).not_to be_nil
        expect(lookup(entry[:oid]).content).to eq(options[:content])
      end
    end

    context 'when a file at that path exists' do
      before do
        options[:file_path] = 'files/executables/ls'
      end

      it 'raises an error' do
        expect { index.create(options) }.to raise_error('A file with this name already exists')
      end
    end

    context 'when content is in base64' do
      before do
        options[:content] = Base64.encode64(options[:content])
        options[:encoding] = 'base64'
      end

      it 'decodes base64' do
        index.create(options)

        entry = index.get(options[:file_path])
        expect(lookup(entry[:oid]).content).to eq(Base64.decode64(options[:content]))
      end
    end

    context 'when content contains CRLF' do
      before do
        repository.autocrlf = :input
        options[:content] = "Hello,\r\nWorld"
      end

      it 'converts to LF' do
        index.create(options)

        entry = index.get(options[:file_path])
        expect(lookup(entry[:oid]).content).to eq("Hello,\nWorld")
      end
    end
  end

  describe '#create_dir' do
    let(:options) do
      {
        file_path: 'newdir'
      }
    end

    context 'when no file or dir at that path exists' do
      it 'creates the dir in the index' do
        index.create_dir(options)

        entry = index.get(options[:file_path] + '/.gitkeep')

        expect(entry).not_to be_nil
      end
    end

    context 'when a file at that path exists' do
      before do
        options[:file_path] = 'files/executables/ls'
      end

      it 'raises an error' do
        expect { index.create_dir(options) }.to raise_error('A file with this name already exists')
      end
    end

    context 'when a directory at that path exists' do
      before do
        options[:file_path] = 'files/executables'
      end

      it 'raises an error' do
        expect { index.create_dir(options) }.to raise_error('A directory with this name already exists')
      end
    end
  end

  describe '#update' do
    let(:options) do
      {
        content: 'Lorem ipsum...',
        file_path: 'README.md'
      }
    end

    context 'when no file at that path exists' do
      before do
        options[:file_path] = 'documents/story.txt'
      end

      it 'raises an error' do
        expect { index.update(options) }.to raise_error("A file with this name doesn't exist")
      end
    end

    context 'when a file at that path exists' do
      it 'updates the file in the index' do
        index.update(options)

        entry = index.get(options[:file_path])

        expect(lookup(entry[:oid]).content).to eq(options[:content])
      end

      it 'preserves file mode' do
        options[:file_path] = 'files/executables/ls'

        index.update(options)

        entry = index.get(options[:file_path])

        expect(entry[:mode]).to eq(0100755)
      end
    end
  end

  describe '#move' do
    let(:options) do
      {
        content: 'Lorem ipsum...',
        previous_path: 'README.md',
        file_path: 'NEWREADME.md'
      }
    end

    context 'when no file at that path exists' do
      it 'raises an error' do
        options[:previous_path] = 'documents/story.txt'

        expect { index.move(options) }.to raise_error("A file with this name doesn't exist")
      end
    end

    context 'when a file at the new path already exists' do
      it 'raises an error' do
        options[:file_path] = 'CHANGELOG'

        expect { index.move(options) }.to raise_error("A file with this name already exists")
      end
    end

    context 'when a file at that path exists' do
      it 'removes the old file in the index' do
        index.move(options)

        entry = index.get(options[:previous_path])

        expect(entry).to be_nil
      end

      it 'creates the new file in the index' do
        index.move(options)

        entry = index.get(options[:file_path])

        expect(entry).not_to be_nil
        expect(lookup(entry[:oid]).content).to eq(options[:content])
      end

      it 'preserves file mode' do
        options[:previous_path] = 'files/executables/ls'

        index.move(options)

        entry = index.get(options[:file_path])

        expect(entry[:mode]).to eq(0100755)
      end
    end
  end

  describe '#delete' do
    let(:options) do
      {
        file_path: 'README.md'
      }
    end

    context 'when no file at that path exists' do
      before do
        options[:file_path] = 'documents/story.txt'
      end

      it 'raises an error' do
        expect { index.delete(options) }.to raise_error("A file with this name doesn't exist")
      end
    end

    context 'when a file at that path exists' do
      it 'removes the file in the index' do
        index.delete(options)

        entry = index.get(options[:file_path])

        expect(entry).to be_nil
      end
    end
  end

  describe '#chmod' do
    def entry
      index.get(options[:file_path])
    end

    let(:options) do
      {
        file_path: 'README.md',
        execute_filemode: true
      }
    end

    context 'when no file at that path exists' do
      before do
        options[:file_path] = 'documents/story.txt'
      end

      it 'raises an error' do
        expect { index.chmod(options) }.to raise_error("A file with this name doesn't exist")
      end
    end

    context 'when a file at that path exists' do
      it 'updates the execute file mode' do
        expect { index.chmod(options) }.to change { entry[:mode] }.from(0o100644).to(0o100755)
      end

      it 'leaves the content unchanged' do
        expect { index.chmod(options) }.not_to change { lookup(entry[:oid]).content }
      end
    end
  end

  def lookup(revision)
    repository.rugged.rev_parse(revision)
  end
end
