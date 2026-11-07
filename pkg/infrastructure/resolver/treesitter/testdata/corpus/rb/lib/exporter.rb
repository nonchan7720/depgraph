module Exporter
  def self.call(rows) = rows.map(&:to_h)
end
