require 'spec_helper'

describe GitalyServer::Utils do
  let(:cls) do
    Class.new do
      include GitalyServer::Utils
    end
  end

  describe '.set_utf8!' do
    context 'valid encoding' do
      it 'returns a UTF-8 string' do
        str = 'écoles'

        expect(cls.new.set_utf8!(str.b)).to eq(str)
      end
    end

    context 'invalid encoding' do
      it 'returns a UTF-8 string' do
        str = "\xA9coles".b

        expect { cls.new.set_utf8!(str) }.to raise_error(ArgumentError)
      end
    end
  end
end
