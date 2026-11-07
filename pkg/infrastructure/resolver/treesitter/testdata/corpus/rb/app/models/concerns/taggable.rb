module Taggable
  extend ActiveSupport::Concern

  included do
    has_many :tags
  end

  class_methods do
    def tagged(name) = where(tag: name)
  end
end
