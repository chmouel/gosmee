# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class Gosmee < Formula
  desc "gosmee  - smee.io go client"
  homepage "https://github.com/chmouel/gosmee"
  version "0.0.11"

  on_macos do
    url "https://github.com/chmouel/gosmee/releases/download/0.0.11/gosmee_0.0.11_MacOS_all.tar.gz"
    sha256 "f1840aea2668fee9baa3ede35ea9cc9b34932ded139910982db3164bc3692f2e"

    def install
      bin.install "gosmee" => "gosmee"
      output = Utils.popen_read("SHELL=bash #{bin}/gosmee completion bash")
      (bash_completion/"gosmee").write output
      output = Utils.popen_read("SHELL=zsh #{bin}/gosmee completion zsh")
      (zsh_completion/"_gosmee").write output
      prefix.install_metafiles
    end
  end

  on_linux do
    if Hardware::CPU.intel?
      url "https://github.com/chmouel/gosmee/releases/download/0.0.11/gosmee_0.0.11_Linux_x86_64.tar.gz"
      sha256 "eb1b8091c7f2a0ff794448131aabba9b7162eb2438e10274981c8b09e08391a5"

      def install
        bin.install "gosmee" => "gosmee"
        output = Utils.popen_read("SHELL=bash #{bin}/gosmee completion bash")
        (bash_completion/"gosmee").write output
        output = Utils.popen_read("SHELL=zsh #{bin}/gosmee completion zsh")
        (zsh_completion/"_gosmee").write output
        prefix.install_metafiles
      end
    end
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/chmouel/gosmee/releases/download/0.0.11/gosmee_0.0.11_Linux_arm64.tar.gz"
      sha256 "d51dc38ce41c2c033c7f33cb4cef7284e28c1c5206f0699d827d4894840cf8ec"

      def install
        bin.install "gosmee" => "gosmee"
        output = Utils.popen_read("SHELL=bash #{bin}/gosmee completion bash")
        (bash_completion/"gosmee").write output
        output = Utils.popen_read("SHELL=zsh #{bin}/gosmee completion zsh")
        (zsh_completion/"_gosmee").write output
        prefix.install_metafiles
      end
    end
  end
end
