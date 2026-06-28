class Wisp < Formula
  desc "Tailscale-native terminal: local shell with embedded tsnet egress, no daemon"
  homepage "https://github.com/billzhuang/wisp"
  version "1.0.0"
  url "https://github.com/billzhuang/wisp/releases/download/v1.0.0/wisp_darwin_arm64"
  sha256 "79c15a9cab1e2b4a09318aae4a247e168c1453566e58284584441bc4fcd0c057"

  def install
    bin.install "wisp_darwin_arm64" => "wisp"
  end

  test do
    assert_match "wisp", shell_output("#{bin}/wisp -version")
  end
end
